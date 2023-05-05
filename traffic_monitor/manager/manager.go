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
	"os"
	"os/signal"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_monitor/cache"
	"github.com/apache/trafficcontrol/traffic_monitor/config"
	"github.com/apache/trafficcontrol/traffic_monitor/handler"
	"github.com/apache/trafficcontrol/traffic_monitor/health"
	"github.com/apache/trafficcontrol/traffic_monitor/peer"
	"github.com/apache/trafficcontrol/traffic_monitor/poller"
	"github.com/apache/trafficcontrol/traffic_monitor/threadsafe"
	"github.com/apache/trafficcontrol/traffic_monitor/todata"
	"github.com/apache/trafficcontrol/traffic_monitor/towrap"
)

//
// Start starts the poller and handler goroutines
//
func Start(opsConfigFile string, cfg config.Config, appData config.StaticAppData, trafficMonitorConfigFileName string) error {

	toSession := towrap.NewTrafficOpsSessionThreadsafe(nil, nil, cfg.CRConfigHistoryCount, cfg)

	localStates := peer.NewCRStatesThreadsafe() // this is the local state as discoverer by this traffic_monitor
	fetchCount := threadsafe.NewUint()          // note this is the number of individual caches fetched from, not the number of times all the caches were polled.
	healthIteration := threadsafe.NewUint()
	errorCount := threadsafe.NewUint()

	toData := todata.NewThreadsafe()

	// 各種オブジェクトの初期化処理を行います
	cacheHealthHandler := cache.NewHandler()
	cacheHealthPoller := poller.NewCache(true, cacheHealthHandler, cfg, appData)
	cacheStatHandler := cache.NewPrecomputeHandler(toData)
	cacheStatPoller := poller.NewCache(false, cacheStatHandler, cfg, appData)
	monitorConfigPoller := poller.NewMonitorConfig(cfg.MonitorConfigPollingInterval) // monitor_config_polling_interval_msの設定値
	peerHandler := peer.NewHandler()
	peerPoller := poller.NewPeer(peerHandler, cfg, appData)
	distributedPeerHandler := peer.NewHandler()
	distributedPeerPoller := poller.NewPeer(distributedPeerHandler, cfg, appData)

	// poller/monitorconfig.goのPoll()が呼ばれる
	go monitorConfigPoller.Poll()

	// poller/cache.goのPoll()が呼ばれる(NewCache呼び出し時に第１引数trueなのでチャネルは生成される)
	go cacheHealthPoller.Poll()

	// 設定値`stat_polling=true`の場合
	if cfg.StatPolling {
		// poller/cache.goのPoll()が呼ばれる(NewCache呼び出し時に第１引数falseなのでチャネルは生成されない)
		go cacheStatPoller.Poll()
	}

	// poller/peer.goのPoll()が呼ばれる
	go peerPoller.Poll()

	// 設定値`distributed_polling=true`の場合
	if cfg.DistributedPolling {
		// poller/peer.goのPoll()が呼ばれる
		go distributedPeerPoller.Poll()
	}

	// 設定値`max_events`の値を指定する
	events := health.NewThreadsafeEvents(cfg.MaxEvents)

	// 「chan struct{}」は空のチャネルの定義です
	var cachesChangedForStatMgr chan struct{}
	var cachesChangedForHealthMgr chan struct{}
	var cachesChanged chan struct{}

	// 設定値`stat_polling=true`の場合
	if cfg.StatPolling {
		// Stat系変数の設定
		cachesChangedForStatMgr = make(chan struct{})
		cachesChanged = cachesChangedForStatMgr
	} else {
		// Health系変数の設定
		cachesChangedForHealthMgr = make(chan struct{})
		cachesChanged = cachesChangedForHealthMgr
	}

	peerStates := peer.NewCRStatesPeersThreadsafe(cfg.PeerOptimisticQuorumMin) // each peer's last state is saved in this map
	distributedPeerStates := peer.NewCRStatesPeersThreadsafe(0)

	monitorConfig := StartMonitorConfigManager(
		monitorConfigPoller.ConfigChannel,
		localStates,
		peerStates,
		distributedPeerStates,
		cacheStatPoller.ConfigChannel,
		cacheHealthPoller.ConfigChannel,
		peerPoller.ConfigChannel,
		distributedPeerPoller.ConfigChannel,
		monitorConfigPoller.IntervalChan,
		cachesChanged,
		cfg,
		appData,
		toSession,
		toData,
	)

	// 複数台のTrafficMonitorの統合を行なう関数です。
	// 特定のチャネルを受信したら、起動したgoroutineの中でステータスのマージ処理が行われるようになっています。
	combinedStates, combineStateFunc := StartStateCombiner(events, peerStates, localStates, toData)

	StartPeerManager(
		peerHandler.ResultChannel,
		peerStates,
		events,
		combineStateFunc,
	)

	statInfoHistory, statResultHistory, statMaxKbpses, _, lastKbpsStats, dsStats, statUnpolledCaches, localCacheStatus := StartStatHistoryManager(
		cacheStatHandler.ResultChan(),
		localStates,
		combinedStates,
		toData,
		cachesChangedForStatMgr,
		cfg,
		monitorConfig,
		events,
		combineStateFunc,
	)

	lastHealthDurations, healthHistory, healthUnpolledCaches := StartHealthResultManager(
		cacheHealthHandler.ResultChan(),
		toData,
		localStates,
		monitorConfig,
		fetchCount,
		cfg,
		events,
		localCacheStatus,
		cachesChangedForHealthMgr,
		combineStateFunc,
	)

	StartDistributedPeerManager(
		distributedPeerHandler.ResultChannel, // peer/peer.goのHandleから送信される
		localStates,
		distributedPeerStates,
		events,
		healthUnpolledCaches,
	)

	// 第４引数と第５引数のchanですが、「chan<-」は単方向チャネル型を表します。
	// [] は、Go言語におけるスライス（slice）型を表します。したがって、[]chan<- testChannel は、testChannel型の値を送信することができるチャネル型のスライスを表します。
	if _, err := StartOpsConfigManager(
		opsConfigFile,
		toSession,
		toData,
		[]chan<- handler.OpsConfig{monitorConfigPoller.OpsConfigChannel},                // handler.OpsConfig型のmonitorConfigPoller.OpsConfigChannelチャネルの受信を表す
		[]chan<- towrap.TrafficOpsSessionThreadsafe{monitorConfigPoller.SessionChannel}, // towrap.TrafficOpsSessionThreadsafe型のmonitorConfigPoller.SessionChannelチャネルの受信を表す
		localStates,
		peerStates,
		distributedPeerStates,
		combinedStates,
		statInfoHistory,
		statResultHistory,
		statMaxKbpses,
		healthHistory,
		lastKbpsStats,
		dsStats,
		events,
		appData,
		cacheHealthPoller.Config.Interval,
		lastHealthDurations,
		fetchCount,
		healthIteration,
		errorCount,
		localCacheStatus,
		statUnpolledCaches,
		healthUnpolledCaches,
		monitorConfig,
		cfg,
	); err != nil {
		return fmt.Errorf("starting ops config manager: %v", err)
	}

	// --configで指定されたファイルを読み込みます。SIGHUPを受信したら再読み込みするように仕掛けます。
	if err := startMonitorConfigFilePoller(trafficMonitorConfigFileName); err != nil {
		return fmt.Errorf("starting monitor config file poller: %v", err)
	}

	healthTickListener(cacheHealthPoller.TickChan, healthIteration)
	return nil
}

// healthTickListener listens for health ticks, and writes to the health iteration variable. Does not return.
func healthTickListener(cacheHealthTick <-chan uint64, healthIteration threadsafe.Uint) { // cacheHealthTickは受信専用チャネル

	// TODO: どこからcacheHealthTickチャネルが送信されてくるのか?
	for i := range cacheHealthTick { // cacheHealthTickチャネルから新しい値が受信されるまで待機し、値が受信された場合は、healthIteration 変数にその値を設定します。
		// healthIterationには「Uint{val: &v} 」という構造体が格納されていて、1つしかフィールドが存在しない場合にはhealthIteration.Set(i)と記述しても問題ないようです。2つ以上の場合には明示的なフィールドの指定が必要です
		healthIteration.Set(i)
	}

}

// filenameには--configで指定されたファイル名が入ります。
func startMonitorConfigFilePoller(filename string) error {

	// 無名関数を代入するクロージャー変数
	onChange := func(bytes []byte, err error) {

		if err != nil {
			log.Errorf("monitor config file poll, polling file '%v': %v", filename, err)
			return
		}

		cfg, err := config.LoadBytes(bytes)
		if err != nil {
			log.Errorf("monitor config file poll, loading bytes '%v' from '%v': %v", string(bytes), filename, err)
			return
		}

		if err := log.InitCfg(cfg); err != nil {
			log.Errorf("monitor config file poll, getting log writers '%v': %v", filename, err)
			return
		}
	}

	// 指定されたファイルの内容をbytesに保存する
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	// 設定ファイルの読み込みが行われる
	onChange(bytes, nil)

	// 下記関数ではSIGHUPを受信するとonChangeが実行される仕組みとなっている
	startSignalFileReloader(filename, unix.SIGHUP, onChange)

	return nil
}

// signalFileReloader starts a goroutine which, when the given signal is received, attempts to load the given file and calls the given function with its bytes or error. There is no way to stop the goroutine or stop listening for signals, thus this should not be called if it's ever necessary to stop handling or change the listened file. The initialRead parameter determines whether the given handler is called immediately with an attempted file read (without a signal).
func startSignalFileReloader(filename string, sig os.Signal, f func([]byte, error)) {

	// goroutineで起動する
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, sig)  // 指定されたシグナルを受信したら動き出す
		for range c {
			f(ioutil.ReadFile(filename)) // 指定された無名関数が実行される
		}
	}()
}

// ipv6CIDRStrToAddr takes an IPv6 CIDR string, e.g. `2001:DB8::1/32` returns `2001:DB8::1`.
// It does not verify cidr is a valid CIDR or IPv6. It only removes the first slash and everything after it, for performance.
func ipv6CIDRStrToAddr(cidr string) string {
	i := strings.Index(cidr, `/`)
	if i == -1 {
		return cidr
	}
	return cidr[:i]
}
