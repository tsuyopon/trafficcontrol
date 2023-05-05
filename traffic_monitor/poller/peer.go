package poller

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
	"bytes"
	"io"
	"math/rand"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_monitor/config"
	"github.com/apache/trafficcontrol/traffic_monitor/handler"
)

type PeerPoller struct {
	Config         PeerPollerConfig
	ConfigChannel  chan PeerPollerConfig
	GlobalContexts map[string]interface{}
	Handler        handler.Handler
}

type PeerPollConfig struct {
	URLs     []string
	Timeout  time.Duration
	Format   string
	PollType string
}

func (c PeerPollConfig) Equals(other PeerPollConfig) bool {
	if len(c.URLs) != len(other.URLs) {
		return false
	}
	for i, v := range c.URLs {
		if v != other.URLs[i] {
			return false
		}
	}
	return c.Timeout == other.Timeout && c.Format == other.Format && c.PollType == other.PollType
}

type PeerPollerConfig struct {
	Urls        map[string]PeerPollConfig
	Interval    time.Duration
	NoKeepAlive bool
}

// NewPeer creates and returns a new PeerPoller.
func NewPeer(
	handler handler.Handler,
	cfg config.Config,
	appData config.StaticAppData,
) PeerPoller {

	// PeerPollerオブジェクトが返却される
	return PeerPoller{
		ConfigChannel:  make(chan PeerPollerConfig),      // チャネル
		GlobalContexts: GetGlobalContexts(cfg, appData),
		Handler:        handler,
	}

}

type PeerPollInfo struct {
	NoKeepAlive bool
	Interval    time.Duration
	ID          string
	PeerPollConfig
}

// peerPollerやdistributedPeerPollerからそれぞれ呼ばれる可能性がある
func (p PeerPoller) Poll() {

	killChans := map[string]chan<- struct{}{}

	// ConfigChannelを受信したら実行する。
	for newConfig := range p.ConfigChannel {

		// 設定差分を確認して、削除したいポーリングがあればdeletionsに、追加したいポーリングがあればadditionsに情報が含まれる
		deletions, additions := diffPeerConfigs(p.Config, newConfig)

		// killChanに送信することにより対象のポーリングを停止し、チャネルを削除する
		for _, id := range deletions {

			// 削除したいPoll Idを指定して、killChanチャネルに送信する
			killChan := killChans[id]
			go func() { killChan <- struct{}{} }() // go - we don't want to wait for old polls to die.

			// チャネルの削除
			delete(killChans, id)
		}

		// 新しいポーリング対象がある場合には実行される
		for _, info := range additions {

			// killChanを用意しておく
			kill := make(chan struct{})
			killChans[info.ID] = kill

			if _, ok := pollers[info.PollType]; !ok {
				if info.PollType != "" { // don't warn for missing parameters
					log.Warnln("CachePoller.Poll: poll type '" + info.PollType + "' not found, using default poll type '" + DefaultPollerType + "'")
				}
				info.PollType = DefaultPollerType  // デフォルトは「http」
			}
			pollerObj := pollers[info.PollType]

			// ポーリング用の設定オブジェクト
			pollerCfg := PollerConfig{
				Timeout:     info.Timeout,
				NoKeepAlive: info.NoKeepAlive,
				PollerID:    info.ID,
			}

			pollerCtx := interface{}(nil)
			// 下記は info.PollType = http の場合にだけ条件分岐に突入する
			if pollerObj.Init != nil {
				// 下記Init()はpoller/poller_type_http.goのhttpInit()が呼ばれます。
				pollerCtx = pollerObj.Init(pollerCfg, p.GlobalContexts[info.PollType])
			}

			// HTTPポーリング処理や結果の解析処理は下記で行います。必要な数だけここのgoroutine(Polling関数)が呼ばれます。これはkill(killChans)チャネルに送信することで停止できます。
			go peerPoller(info.Interval, info.ID, info.URLs, info.Format, p.Handler, pollerObj.Poll, pollerCtx, kill)
		}

		// 設定オブジェクトを差し替える
		p.Config = newConfig
	}
}

func peerPoller(
	interval time.Duration,
	id string,
	urls []string,
	format string,
	handler handler.Handler,
	pollFunc PollerFunc,
	pollCtx interface{},
	die <-chan struct{},
) {
	pollSpread := time.Duration(rand.Float64()*float64(interval/time.Nanosecond)) * time.Nanosecond
	time.Sleep(pollSpread)
	tick := time.NewTicker(interval)
	lastTime := time.Now()
	urlI := rand.Intn(len(urls)) // start at a random URL index in order to help spread load
	for {
		select {
		case <-tick.C:

			// 現在時刻から最終更新時刻(lastTime)の差分を取得してrealIntervalとして、指定したIntervalが経過していたらログを出力する
			realInterval := time.Now().Sub(lastTime)
			if realInterval > interval+(time.Millisecond*100) {
				log.Debugf("Intended Duration: %v Actual Duration: %v\n", interval, realInterval)
			}

			// タイマーによる最終実行時刻をlastTimeに保存しておく
			lastTime = time.Now()

			pollID := atomic.AddUint64(&pollNum, 1)
			pollFinishedChan := make(chan uint64)
			log.Debugf("peer poll %v %v start\n", pollID, time.Now())

			urlString := urls[urlI]
			urlI = (urlI + 1) % len(urls)
			urlParsed, err := url.Parse(urlString)
			if err != nil {
				// this should never happen because TM creates the URL
				log.Errorf("parsing peer poller URL %s: %s", urlString, err.Error())
			}

			host := urlParsed.Host

			// ここでポーリングが行われ、その結果が帰ってくる
			// typeが「http」の場合httpPoll、「noop」の場合noopPollが呼ばれる (AddPollerTypeで指定した値)
			bts, reqEnd, reqTime, err := pollFunc(pollCtx, urlString, host, pollID)

			// ポーリングにより取得した結果を読み込む
			rdr := io.Reader(nil)
			if bts != nil {
				rdr = bytes.NewReader(bts) // TODO change handler to take bytes? Benchmark?
			}

			log.Debugf("peer poll %v %v poller end\n", pollID, time.Now())

			// Handleはここで実行される(Handle関数自体はtraffic_monitor/cache/cache.goやtraffic_monitor/peer/peer.goで定義されている)。定義位置と実行位置が乖離しているのでわかりにくいので注意すること
			// HandleはHTTPポーリングのレスポンスの解析処理が行われる
			go handler.Handle(id, rdr, format, reqTime, reqEnd, err, pollID, false, pollCtx, pollFinishedChan)

			// peerの場合にはStartPeerManager()内のgoroutineから、distributedPeerの場合にはStartDistributedPeerManager()に内のgoroutineから送信されます
			<-pollFinishedChan

		case <-die: // killChanを受け取った場合には、タイマーを停止してこの関数をそのままreturnする。
			tick.Stop()
			return
		}
	}
}

// diffPeerConfigs takes the old and new configs, and returns a list of deleted IDs, and a list of new polls to do
func diffPeerConfigs(old PeerPollerConfig, new PeerPollerConfig) ([]string, []PeerPollInfo) {

	deletions := []string{}
	additions := []PeerPollInfo{}

	// Intervalが変わっている または NoKeepAlive設定が変わっている 場合には
	// 古いデータは全て削除対象とする。新しいデータは全てオブジェクト生成対象とする
	if old.Interval != new.Interval || old.NoKeepAlive != new.NoKeepAlive {

		// 削除対象のURLが含まれるIDを取得する
		for id, _ := range old.Urls {
			deletions = append(deletions, id)
		}

		// 追加対象のURLが含まれるIDを含んだPeerPollInfoオブジェクトを生成する
		for id, pollCfg := range new.Urls {
			additions = append(additions, PeerPollInfo{
				Interval:       new.Interval,
				NoKeepAlive:    new.NoKeepAlive,
				ID:             id,
				PeerPollConfig: pollCfg,
			})
		}

		// returnすることに注意
		return deletions, additions

	}


	// 古いURLに含まれるIDが、新しいURLに含まれるIDに存在するかどうかをチェックする
	// 存在しなければdeletionsに追加、存在していればdeletionsに追加し、新しいオブジェクトを生成する
	for id, oldPollCfg := range old.Urls {
		newPollCfg, newIdExists := new.Urls[id]
		if !newIdExists {
			deletions = append(deletions, id)
		} else if !newPollCfg.Equals(oldPollCfg) {
			deletions = append(deletions, id)
			additions = append(additions, PeerPollInfo{
				Interval:       new.Interval,
				NoKeepAlive:    new.NoKeepAlive,
				ID:             id,
				PeerPollConfig: newPollCfg,
			})
		}
	}

	// 新しいURLに含まれるIDが、古いURLに含まれるIDに存在するかどうかをチェックする
	// 存在しなければ新規で追加されるIDだということでadditionに追加する。
	for id, newPollCfg := range new.Urls {
		_, oldIdExists := old.Urls[id]
		if !oldIdExists {
			additions = append(additions, PeerPollInfo{
				Interval:       new.Interval,
				NoKeepAlive:    new.NoKeepAlive,
				ID:             id,
				PeerPollConfig: newPollCfg,
			})
		}
	}

	return deletions, additions
}
