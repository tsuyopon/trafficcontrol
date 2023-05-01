package manager

/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import (
	"fmt"
	"io/ioutil"
	"net"
	"time"

	"golang.org/x/sys/unix"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/lib/go-util"
	"github.com/apache/trafficcontrol/traffic_monitor/config"
	"github.com/apache/trafficcontrol/traffic_monitor/datareq"
	"github.com/apache/trafficcontrol/traffic_monitor/handler"
	"github.com/apache/trafficcontrol/traffic_monitor/health"
	"github.com/apache/trafficcontrol/traffic_monitor/peer"
	"github.com/apache/trafficcontrol/traffic_monitor/srvhttp"
	"github.com/apache/trafficcontrol/traffic_monitor/threadsafe"
	"github.com/apache/trafficcontrol/traffic_monitor/todata"
	"github.com/apache/trafficcontrol/traffic_monitor/towrap"

	jsoniter "github.com/json-iterator/go"
)

// StartOpsConfigManager starts the ops config manager goroutine, returning the (threadsafe) variables which it sets.
// Note the OpsConfigManager is in charge of the httpServer, because ops config changes trigger server changes. If other things needed to trigger server restarts, the server could be put in its own goroutine with signal channels
func StartOpsConfigManager(
	opsConfigFile string,
	toSession towrap.TrafficOpsSessionThreadsafe,
	toData todata.TODataThreadsafe,
	opsConfigChangeSubscribers []chan<- handler.OpsConfig,
	toChangeSubscribers []chan<- towrap.TrafficOpsSessionThreadsafe,
	localStates peer.CRStatesThreadsafe,
	peerStates peer.CRStatesPeersThreadsafe,
	distributedPeerStates peer.CRStatesPeersThreadsafe,
	combinedStates peer.CRStatesThreadsafe,
	statInfoHistory threadsafe.ResultInfoHistory,
	statResultHistory threadsafe.ResultStatHistory,
	statMaxKbpses threadsafe.CacheKbpses,
	healthHistory threadsafe.ResultHistory,
	lastStats threadsafe.LastStats,
	dsStats threadsafe.DSStatsReader,
	events health.ThreadsafeEvents,
	staticAppData config.StaticAppData,
	healthPollInterval time.Duration,
	lastHealthDurations threadsafe.DurationMap,
	fetchCount threadsafe.Uint,
	healthIteration threadsafe.Uint,
	errorCount threadsafe.Uint,
	localCacheStatus threadsafe.CacheAvailableStatus,
	statUnpolledCaches threadsafe.UnpolledCaches,
	healthUnpolledCaches threadsafe.UnpolledCaches,
	monitorConfig threadsafe.TrafficMonitorConfigMap,
	cfg config.Config,
) (threadsafe.OpsConfig, error) {

	// エラー時に呼ばれる用の無名関数を定義する
	handleErr := func(err error) {
		errorCount.Inc()
		log.Errorf("OpsConfigManager: %v\n", err)
	}

	httpServer := srvhttp.Server{}
	httpsServer := srvhttp.Server{}
	opsConfig := threadsafe.NewOpsConfig()

	// TODO remove change subscribers, give Threadsafes directly to the things that need them. If they only set vars, and don't actually do work on change.
	onChange := func(bytes []byte, err error) {
		if err != nil {
			handleErr(err)
			return
		}

		newOpsConfig := handler.OpsConfig{}
		json := jsoniter.ConfigFastest // TODO make configurable?
		if err = json.Unmarshal(bytes, &newOpsConfig); err != nil {
			handleErr(fmt.Errorf("Could not unmarshal Ops Config JSON: %s\n", err))
			return
		}

		opsConfig.Set(newOpsConfig)

		listenAddress := ":80" // default

		if newOpsConfig.HttpListener != "" {
			listenAddress = newOpsConfig.HttpListener
		}

		// Traffic MonitorのWebAPIエンドポイントと呼び出される関数ハンドラのマッピングを取得する
		endpoints := datareq.MakeDispatchMap(
			opsConfig,
			toSession,
			localStates,
			peerStates,
			distributedPeerStates,
			combinedStates,
			statInfoHistory,
			statResultHistory,
			statMaxKbpses,
			healthHistory,
			dsStats,
			events,
			staticAppData,
			healthPollInterval,
			lastHealthDurations,
			fetchCount,
			healthIteration,
			errorCount,
			toData,
			localCacheStatus,
			lastStats,
			statUnpolledCaches,
			healthUnpolledCaches,
			monitorConfig,
			cfg.StatPolling,
			cfg.DistributedPolling,
		)

		// If the HTTPS Listener is defined in the traffic_ops.cfg file then it creates the HTTPS endpoint and the corresponding HTTP endpoint as a redirect
		// 設定「httpsListener」が空でなければ
		if newOpsConfig.HttpsListener != "" {
			httpsListenAddress := newOpsConfig.HttpsListener

			// StaticFileDir="/opt/traffic_monitor/static/"
			err = httpServer.RunHTTPSRedirect(listenAddress, httpsListenAddress, cfg.ServeReadTimeout, cfg.ServeWriteTimeout, cfg.StaticFileDir)
			if err != nil {
				handleErr(fmt.Errorf("MonitorConfigPoller: error creating HTTP server: %s\n", err))
				return
			}

			// HTTP*S*サーバを起動する
			err = httpsServer.Run(endpoints, httpsListenAddress, cfg.ServeReadTimeout, cfg.ServeWriteTimeout, cfg.StaticFileDir, true, newOpsConfig.CertFile, newOpsConfig.KeyFile)
			if err != nil {
				handleErr(fmt.Errorf("MonitorConfigPoller: error creating HTTPS server: %s\n", err))
				return
			}
		} else {
			// HTTPサーバを起動する
			err = httpServer.Run(endpoints, listenAddress, cfg.ServeReadTimeout, cfg.ServeWriteTimeout, cfg.StaticFileDir, false, "", "")
			if err != nil {
				handleErr(fmt.Errorf("MonitorConfigPoller: error creating HTTP server: %s\n", err))
				return
			}
		}

		// TODO config? parameter?
		useCache := false
		trafficOpsRequestTimeout := time.Second * time.Duration(10)
		var toAddr net.Addr
		var toLoginCount uint64

		// fixed an issue here where traffic_monitor loops forever, doing nothing useful if traffic_ops is down,
		// and would never logging in again.  since traffic_monitor  is just starting up here, keep retrying until traffic_ops is reachable and a session can be established.
		backoff, err := util.NewBackoff(cfg.TrafficOpsMinRetryInterval, cfg.TrafficOpsMaxRetryInterval, util.DefaultFactor)
		if err != nil {
			log.Errorf("possible invalid backoff arguments, will use a fixed sleep interval: %v, will use a fallback duration: %v", err, util.ConstantBackoffDuration)
			// use a fallback constant duration.
			backoff = util.NewConstantBackoff(util.ConstantBackoffDuration)
		}
		for {
			err = toSession.Update(newOpsConfig.Url, newOpsConfig.Username, newOpsConfig.Password, newOpsConfig.Insecure, staticAppData.UserAgent, useCache, trafficOpsRequestTimeout)
			if err != nil {
				handleErr(fmt.Errorf("MonitorConfigPoller: error instantiating Session with traffic_ops (%v): %s\n", toAddr, err))
				duration := backoff.BackoffDuration()
				log.Errorf("retrying in %v\n", duration)
				time.Sleep(duration)

				if toSession.BackupFileExists() && (toLoginCount >= cfg.TrafficOpsDiskRetryMax) {
					newOpsConfig.UsingDummyTO = true
					log.Errorf("error instantiating authenticated session with Traffic Ops, backup disk files exist, continuing with unauthenticated session")
					break
				}

				toLoginCount++
				continue
			} else {
				newOpsConfig.UsingDummyTO = false
				break
			}
		}
		opsConfig.Set(newOpsConfig)

		if cdn, err := toSession.MonitorCDN(staticAppData.Hostname); err != nil {
			// エラーがある場合
			handleErr(fmt.Errorf("getting CDN name from Traffic Ops, using config CDN '%s': %s\n", newOpsConfig.CdnName, err))
		} else {
			// エラーない場合

			// 「設定されたCdnNameの値が空ではない」かつ「設定されたCdnNameの値とTrafficOpsから取得したcdnの値が一致しない」場合には警告を取得する。
			if newOpsConfig.CdnName != "" && newOpsConfig.CdnName != cdn {
				log.Warnf("%s Traffic Ops CDN '%s' doesn't match config CDN '%s' - using Traffic Ops CDN\n", staticAppData.Hostname, cdn, newOpsConfig.CdnName)
			}

			// TrafficOpsから取得できた場合には、その値をnewOpsConfig.CdnNameに書き出しておく
			newOpsConfig.CdnName = cdn
		}

		// These must be in a goroutine, because the monitorConfigPoller tick sends to a channel this select listens for. Thus, if we block on sends to the monitorConfigPoller, we have a livelock race condition.
		// More generically, we're using goroutines as an infinite chan buffer, to avoid potential livelocks

		for _, subscriber := range opsConfigChangeSubscribers {
			// 以下のgoroutineは無名関数を即時実行しています。 
			//  cf: https://qiita.com/hir1524/items/a270b00c420ed96f02f0#%E5%8D%B3%E6%99%82%E9%96%A2%E6%95%B0
			// 即時実行なので最後の(subscriber)というのはその手前の無名関数の引数に指定される値です。
			go func(s chan<- handler.OpsConfig) { s <- newOpsConfig }(subscriber)
		}
		for _, subscriber := range toChangeSubscribers {
			go func(s chan<- towrap.TrafficOpsSessionThreadsafe) { s <- toSession }(subscriber)
		}
	}
	// onChangeの無銘関数定義はここまで

	// ファイルとして--opsCfgに指定されたファイルが読み込めるかどうかの確認
	bytes, err := ioutil.ReadFile(opsConfigFile)
	if err != nil {
		return opsConfig, err
	}

	// 同一関数内で定義した無名関数の定義がonChangeに設定されているのでそれを呼び出す。
	onChange(bytes, err)

	// SIGHUPを受信したら「--opsCfg」として指定されたファイルの再読み込みを行う
	startSignalFileReloader(opsConfigFile, unix.SIGHUP, onChange)

	return opsConfig, nil
}
