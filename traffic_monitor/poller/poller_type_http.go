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
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_monitor/config"
)

const PollerTypeHTTP = "http"

// golangではinit関数はパッケージインポート時に明示的に実行を指定しなくても実行されます。つまり、下記のinitは読み込み時に実行されます。
// 注意点として、同じパッケージ内に複数のinit()関数がある場合、実行の順序が保証されません。また、同じパッケージを複数回インポートしても、init()関数は1回しか実行されません。
func init() {
	AddPollerType(PollerTypeHTTP, httpGlobalInit, httpInit, httpPoll)
}

func httpGlobalInit(cfg config.Config, appData config.StaticAppData) interface{} {

	sharedClient := &http.Client{
		Transport: &http.Transport{},
		Timeout:   cfg.HTTPTimeout,
	}

	return &HTTPPollGlobalCtx{
		UserAgent:    appData.UserAgent,
		Client:       sharedClient,
		FormatAccept: cfg.HTTPPollingFormat,
	}

}

// ポーリングのための初期化処理を行う
func httpInit(cfg PollerConfig, globalCtxI interface{}) interface{} {
	gctx := (globalCtxI).(*HTTPPollGlobalCtx)

	if cfg.Timeout != 0 || cfg.NoKeepAlive { // if the timeout isn't explicitly set, use the template value.
		clientCopy := *gctx.Client
		gctx.Client = &clientCopy // copy the client, so it's reused by pollers who DO use the default timeout/keepalive

		if cfg.Timeout != 0 {
			gctx.Client.Timeout = cfg.Timeout
		}

		if cfg.NoKeepAlive {
			transportI := http.DefaultTransport
			transport, ok := transportI.(*http.Transport)
			if !ok {
				log.Errorf("failed to set NoKeepAlive for poller ID '%s': http.DefaultTransport expected type *http.Transport actual %T\n", cfg.PollerID, transportI)
			} else {
				transport.DisableKeepAlives = cfg.NoKeepAlive
				gctx.Client.Transport = transport
				log.Infof("Setting transport.DisableKeepAlives %t for %s\n", transport.DisableKeepAlives, cfg.PollerID)
			}
		}

	}

	return &HTTPPollCtx{
		Client:       gctx.Client,
		UserAgent:    gctx.UserAgent,
		NoKeepAlive:  cfg.NoKeepAlive,
		PollerID:     cfg.PollerID,
		FormatAccept: gctx.FormatAccept,
	}
}

type HTTPPollGlobalCtx struct {
	Client       *http.Client
	UserAgent    string
	FormatAccept string
}

type HTTPPollCtx struct {
	Client       *http.Client
	UserAgent    string
	NoKeepAlive  bool
	PollerID     string
	HTTPHeader   http.Header
	FormatAccept string
}

// memo: http://<IP>:80/_atstats?application=system&inf.name=eth0 へのアクセスはここを経由する。
// HTTPへのリクエストを行う (HTTP Pollingの主要処理はここで行われる)
func httpPoll(ctxI interface{}, url string, host string, pollID uint64) ([]byte, time.Time, time.Duration, error) {

	// オブジェクトの生成
	ctx := (ctxI).(*HTTPPollCtx)

	// GETリクエストオブジェクトにURLを指定する
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, time.Now(), 0, errors.New("creating HTTP request: " + err.Error())
	}

	// User-Agentを付与
	req.Header.Set("User-Agent", ctx.UserAgent)

	// ctx.NoKeepAlive設定がfalseの場合には、Connectionヘッダを付与してkeep-aliveさせる
	if !ctx.NoKeepAlive {
		req.Header.Set("Connection", "keep-alive")
	}

	// リクエストにAcceptヘッダを付与
	req.Header.Set("Accept", ctx.FormatAccept)

	// リクエストにHostヘッダを付与
	req.Host = host

	startReq := time.Now()

	// HTTPリクエストを行う
	resp, err := ctx.Client.Do(req)
	if err != nil {
		reqEnd := time.Now()
		reqTime := reqEnd.Sub(startReq) // note this is the time to transfer the entire body, not just the roundtrip
		return nil, reqEnd, reqTime, fmt.Errorf("id %v url %v fetch error: %v", ctx.PollerID, url, err)
	}
	defer resp.Body.Close()

	// 200番台以外のステータスコードはエラー
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		reqEnd := time.Now()
		reqTime := reqEnd.Sub(startReq) // note this is the time to transfer the entire body, not just the roundtrip
		return nil, reqEnd, reqTime, fmt.Errorf("id %v url %v fetch error: bad HTTP status: %v", ctx.PollerID, url, resp.StatusCode)
	}

	// レスポンスを読み込む
	bts, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		reqEnd := time.Now()
		reqTime := reqEnd.Sub(startReq) // note this is the time to transfer the entire body, not just the roundtrip
		return nil, reqEnd, reqTime, fmt.Errorf("id %v url %v fetch error: reading body: %v", ctx.PollerID, url, err)
	}

	// 終了処理
	reqEnd := time.Now()
	reqTime := reqEnd.Sub(startReq) // note this is the time to transfer the entire body, not just the roundtrip
	ctx.HTTPHeader = resp.Header.Clone()

	return bts, reqEnd, reqTime, nil
}
