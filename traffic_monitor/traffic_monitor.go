package main

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
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_monitor/config"
	"github.com/apache/trafficcontrol/traffic_monitor/manager"
)

// GitRevision is the git revision of the app. The app SHOULD always be built with this set via the `-X` flag.
// 出力サンプル
//   $ git rev-parse HEAD
//   8d406d49fed4cdab7737946bdb0009c4224dfbe8
var GitRevision = "No Git Revision Specified. Please build with '-X main.GitRevision=${git rev-parse HEAD}'"

// BuildTimestamp is the time the app was built. The app SHOULD always be built with this set via the `-X` flag.
// 出力サンプル
//   $ date +'%Y-%M-%dT%H:%M:%S'
//   2023-24-29T07:24:45
var BuildTimestamp = "No Build Timestamp Specified. Please build with '-X main.BuildTimestamp=`date +'%Y-%M-%dT%H:%M:%S'`"

// 設定に応じた出力先の設定と、ログオブジェクトの初期化が行われる
func InitAccessCfg(cfg config.Config) error {
	accessW, err := config.GetAccessLogWriter(cfg)
	if err != nil {
		return err
	}
	log.InitAccess(accessW)
	return nil
}

func main() {

	// 現在のシステム上で利用可能なCPUの数をruntime.NumCPU()で取得し、runtime.GOMAXPROCSによってプログラムで利用可能な最大スレッド数を計算します
	runtime.GOMAXPROCS(runtime.NumCPU())

	staticData, err := config.GetStaticAppData(Version, GitRevision, BuildTimestamp)
	if err != nil {
		fmt.Printf("Error starting service: failed to get static app data: %v\n", err)
		os.Exit(1)
	}

	// --opsCfgと--configは必須で指定が必要名設定ファイルです。
	// --opsCfgはリクエストされるTrafficOpsに関する設定で、--configはtraffic_monitorバイナリ自体の設定の指定です。
	// レポジトリ直下のサンプルとしては下記で確認できます。
	//   
	//   $ find . -name traffic_ops.cfg      // --opsCfgに指定される
	//   ./traffic_monitor/traffic_ops.cfg
	//   ./traffic_monitor/conf/traffic_ops.cfg
	//   
	//   $ find . -name traffic_monitor.cfg  // --configに指定される
	//   ./traffic_monitor/traffic_monitor.cfg
	//   ./traffic_monitor/conf/traffic_monitor.cfg
	//   ./infrastructure/cdn-in-a-box/traffic_monitor/traffic_monitor.cfg
	//
	opsConfigFile := flag.String("opsCfg", "", "The traffic ops config file")            // --opsCfgオプション
	configFileName := flag.String("config", "", "The Traffic Monitor config file path")  // --configオプション
	flag.Parse()

	// --opsCfgが指定されていなければエラー
	if *opsConfigFile == "" {
		fmt.Println("Error starting service: The --opsCfg argument is required")
		os.Exit(1)
	}

	// TODO add hot reloading (like opsConfigFile)?
	// --configが指定されていない場合にはデフォルト設定が有効になるようになっている
	cfg, err := config.Load(*configFileName)
	if err != nil {
		fmt.Printf("Error starting service: failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 各種デバッグレベルに応じたログの初期化
	if err := log.InitCfg(cfg); err != nil {
		fmt.Printf("Error starting service: failed to create log writers: %v\n", err)
		os.Exit(1)
	}

	// 設定情報を元にして「標準出力、標準エラー出力、何も出力しない、指定パスへの書き出しするか」のいずれかを決定して、ログ操作の初期化をする
	if err := InitAccessCfg(cfg); err != nil {
		fmt.Printf("Error starting service: failed to create access log writer: %v\n", err)
		os.Exit(1)
	}

	if cfg.ShortHostnameOverride != "" {   // short_hostname_overrideの設定が指定されている場合
		// TODO: この値は一体何に使うんだ? なぜショートにする意味があるんだ?
		staticData.Hostname = cfg.ShortHostnameOverride
	}

	// Go 1.20未満ではGoでrandを使う場合には忘れずにSeedを設定しなければならなかったとのこと。おそらくその名残ではないかと考えられる。
	// cf. https://makiuchi-d.github.io/2017/09/09/qiita-9c4af327bc8502cdcdce.ja.html
	rand.Seed(time.Now().UnixNano())
	log.Infof("Starting with config %+v\n", cfg)

	// traffic_monitorのメイン処理
	err = manager.Start(*opsConfigFile, cfg, staticData, *configFileName)
	if err != nil {
		fmt.Printf("Error starting service: failed to start managers: %v\n", err)
		os.Exit(1)
	}
}
