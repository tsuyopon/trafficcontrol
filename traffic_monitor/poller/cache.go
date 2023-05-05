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
	"runtime"
	"sync/atomic"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_monitor/config"
	"github.com/apache/trafficcontrol/traffic_monitor/handler"
)

type CachePoller struct {
	Config         CachePollerConfig
	ConfigChannel  chan CachePollerConfig
	TickChan       chan uint64
	GlobalContexts map[string]interface{}
	Handler        handler.Handler
}

type PollConfig struct {
	URL      string
	URLv6    string
	Host     string
	Timeout  time.Duration
	Format   string
	PollType string
}

type CachePollerConfig struct {
	Urls            map[string]PollConfig
	Interval        time.Duration
	NoKeepAlive     bool
	PollingProtocol config.PollingProtocol
}

// NewCache creates and returns a new CachePoller.
// If tick is false, CachePoller.TickChan() will return nil.
// CachePollerオブジェクトを返却する
func NewCache(
	tick bool,
	handler handler.Handler,
	cfg config.Config,
	appData config.StaticAppData,
) CachePoller {

	var tickChan chan uint64

	// 引数がtrueならばtickChanチャネルを生成する
	if tick {
		tickChan = make(chan uint64)
	}

	return CachePoller{
		TickChan:      tickChan,
		ConfigChannel: make(chan CachePollerConfig),
		Config: CachePollerConfig{
			PollingProtocol: cfg.CachePollingProtocol,
		},
		GlobalContexts: GetGlobalContexts(cfg, appData),
		Handler:        handler,
	}
}

var pollNum uint64

type CachePollInfo struct {
	NoKeepAlive     bool
	Interval        time.Duration
	ID              string
	PollingProtocol config.PollingProtocol
	PollConfig
}


func (p CachePoller) Poll() {
	// killChans配列ですが、range addtionsの中でこの配列にチャネルを新規登録し、その後の処理でgo pollerに引き渡して、キャンセル用チャネルとして利用されます。
	// なお、range deletionsの中ではdiffConfigsでdeletionsと判定された特定のidからkillChans配列から取得してkillChanに格納して、キャンセル用として送信しています。
	killChans := map[string]chan<- struct{}{}

	// StartMonitorConfigManager()経由でp.ConfigChannelにチャネルに設定情報データが送信されてきたら下記のfor文が実行される
	// つまり、定期的な設定情報を受信したら、ポーリングの追加・削除処理をここで行う。
	for newConfig := range p.ConfigChannel {

		// 古い設定と新しい設定を比較します。なくなった設定はdeletionsに、新しく追加した設定はadditionsに追加されます。。
		deletions, additions := diffConfigs(p.Config, newConfig)

		// deletionsへの処理
		for _, id := range deletions {
			killChan := killChans[id]
			
			// このkillChanに送付することでpoller()のdie変数がチャネル受信することになります。
			go func() { killChan <- struct{}{} }() // go - we don't want to wait for old polls to die.
			delete(killChans, id)
		}

		// additionsへの処理
		for _, info := range additions {
			kill := make(chan struct{})
			killChans[info.ID] = kill

			// pollersはこのファイルでどこでも宣言されていません。pollers自体はpoller_types.goのソースコードで宣言されています。
			// これはなぜ参照できるかというと同一パッケージ内であれば(先頭に宣言された「package poller」)、異なるファイルでも非公開関数や変数を参照できるらしい。
			// see: https://ryochack.hatenadiary.org/entry/20120115/1326567659
			if _, ok := pollers[info.PollType]; !ok {
				if info.PollType != "" { // don't warn for missing parameters
					log.Warnln("CachePoller.Poll: poll type '" + info.PollType + "' not found, using default poll type '" + DefaultPollerType + "'")
				}

				// DefaultPollerTypeは「http」タイプとなる
				info.PollType = DefaultPollerType
			}

			// オブジェクトを取得する
			pollerObj := pollers[info.PollType]

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

			// ここにp.Handlerで実行するハンドラが渡されている。peer/peer.goのHandle()などはここで引き渡される
			go poller(info.Interval, info.ID, info.PollingProtocol, info.URL, info.URLv6, info.Host, info.Format, p.Handler /* ハンドラ */, pollerObj.Poll, pollerCtx, kill /* dieチャネル */)

		}

		p.Config = newConfig
	}
}

// TODO iterationCount and/or p.TickChan?
// この関数は poller/cache.go: Poll()からのみ呼ばれる
func poller(
	interval time.Duration,
	id string,
	pollingProtocol config.PollingProtocol,
	url string,
	url6 string,
	host string,
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
	oscillateProtocols := false

	if pollingProtocol == config.Both {
		oscillateProtocols = true
	}

	usingIPv4 := pollingProtocol != config.IPv6Only

	for {
		select {

		// タイマーによる実行となる場合
		case <-tick.C:

			// /_atstatエンドポイントへのリクエストが行われる。
			if (usingIPv4 && url == "") || (!usingIPv4 && url6 == "") {
				usingIPv4 = !usingIPv4
				continue
			}

			// time.Now()関数を使って現在の時刻を取得して、前回タイマー起動時(lastTime)からの経過時間をrealIntervalに格納している
			realInterval := time.Now().Sub(lastTime)

			// realIntervalが指定したintervalを超過した場合にはログを出力する
			if realInterval > interval+(time.Millisecond*100) {
				log.Debugf("Intended Duration: %v Actual Duration: %v\n", interval, realInterval)
			}

			// タイマー起動時刻として現在時刻を保存して、次回の計算でこの値を利用するために保持しておく
			lastTime = time.Now()

			pollID := atomic.AddUint64(&pollNum, 1)
			pollFinishedChan := make(chan uint64)
			log.Debugf("poll %v %v start\n", pollID, time.Now())

			// ポーリングURLをセットする。usingIPv4=falseならIPv6用のURLをpollUrlとしてセットする
			pollUrl := url
			if !usingIPv4 {
				pollUrl = url6
			}

			// ポーリング用の関数が呼ばれる
			// typeが「http」の場合httpPoll、「noop」の場合noopPollが呼ばれる (AddPollerTypeで指定した値。
			bts, reqEnd, reqTime, err := pollFunc(pollCtx, pollUrl, host, pollID)
			rdr := io.Reader(nil)
			if bts != nil {
				rdr = bytes.NewReader(bts) // TODO change handler to take bytes? Benchmark?
			}

			// デバッグログへの出力
			log.Debugf("poll %v %v poller end\n", pollID, time.Now())

			// Handleはここで実行される(Handle関数自体はtraffic_monitor/cache/cache.goやtraffic_monitor/peer/peer.goで定義されている)。定義位置と実行位置が乖離しているのでわかりにくいので注意すること
			go handler.Handle(id, rdr, format, reqTime, reqEnd, err, pollID, usingIPv4, pollCtx, pollFinishedChan)

			if oscillateProtocols {
				usingIPv4 = !usingIPv4
			}

			<-pollFinishedChan  // 有効コードで4行上にあるgo handler.Handleの最後の引数に指定したchannelで処理が終わると、チャネルが送信されるので、ここの受信のwaitが解除される。(タイマー起動による同一処理の重複実行させないための対策だと思われる)

		// dieを受け取った場合
		// Pollingが不要になったら送付されてきます。これはこのファイル(cache.go)のPoll()内でdeletionsがあれば「go func() { killChan <- struct{}{} }()」で実行されることで送信されます。これにより不要なポーリングを破棄させる役割があります
		case <-die:
			tick.Stop()  // Poll()の「go func() { killChan <- struct{}{} }()」はここを実行させるためのもの
			return
		}
	}

}

// 新・旧の設定オブジェクトを比較して、新に旧のURLがなければdeletionsにappendする。逆に旧に新のURLがなければadditionsにappendする。
// diffConfigs takes the old and new configs, and returns a list of deleted IDs, and a list of new polls to do
func diffConfigs(old CachePollerConfig, new CachePollerConfig) ([]string, []CachePollInfo) {
	deletions := []string{}
	additions := []CachePollInfo{}

	if old.Interval != new.Interval || old.NoKeepAlive != new.NoKeepAlive {
		for id, _ := range old.Urls {
			deletions = append(deletions, id)
		}
		for id, pollCfg := range new.Urls {
			additions = append(additions, CachePollInfo{
				Interval:        new.Interval,
				NoKeepAlive:     new.NoKeepAlive,
				ID:              id,
				PollingProtocol: new.PollingProtocol,
				PollConfig:      pollCfg,
			})
		}
		return deletions, additions
	}

	// old.Urlsには"edge", "mid-02", "mid-01"のそれぞれのオブジェクトでイテレーションされる
	for id, oldPollCfg := range old.Urls {
		newPollCfg, newIdExists := new.Urls[id]
		if !newIdExists {
			deletions = append(deletions, id)
		} else if newPollCfg != oldPollCfg {
			deletions = append(deletions, id)
			additions = append(additions, CachePollInfo{
				Interval:        new.Interval,
				NoKeepAlive:     new.NoKeepAlive,
				ID:              id,
				PollingProtocol: new.PollingProtocol,
				PollConfig:      newPollCfg,
			})
		}
	}

	for id, newPollCfg := range new.Urls {
		_, oldIdExists := old.Urls[id]
		if !oldIdExists {
			additions = append(additions, CachePollInfo{
				Interval:        new.Interval,
				NoKeepAlive:     new.NoKeepAlive,
				ID:              id,
				PollingProtocol: new.PollingProtocol,
				PollConfig:      newPollCfg,
			})
		}
	}

	return deletions, additions
}

func stacktrace() []byte {
	initialBufSize := 1024
	buf := make([]byte, initialBufSize)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, len(buf)*2)
	}
}
