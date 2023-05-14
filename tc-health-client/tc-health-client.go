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
	"os"
	"strconv"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/tc-health-client/config"
	"github.com/apache/trafficcontrol/tc-health-client/tmagent"
)

// OSへの戻り値(ReturnCode)
const (
	Success      = 0
	ConfigError  = 166
	RunTimeError = 167
	PidFile      = "/run/tc-health-client.pid"
)

// the BuildTimestamp and Version are set via ld flags
// when the RPM is built, see build/build_rpm.sh
// ここはビルド時にセットされるらしい
// tc-health-client/build/build_rpm.shには下記のようにビルドされているので、ここでBuildTimestampとVersionが格納されていると思われる
//   (例) $ go build -v -gcflags "$gcflags" -ldflags "${ldflags} -X main.BuildTimestamp=$(date +'%Y-%m-%dT%H:%M:%S') -X main.Version=${TC_VERSION}-${BUILD_NUMBER}" -tags "$tags";
var (
	BuildTimestamp = ""
	Version        = ""
)

// tc-health-client
// tc-health-client コマンドはATSで稼働しているホスト上のATSのparentを管理する為に利用される
// このコマンドを実行した際にデフォルトで読み込みされる設定ファイルは「/etc/trafficcontrol/tc-health-client.json」です。
// この設定ファイルを読み込んだ後に、Traffic Monitorのリストを取得するためにTrafficOpsへとポーリングします。
//
// 説明: https://sourcegraph.com/github.com/apache/trafficcontrol/-/blob/tc-health-client/README.md
func main() {

	// オプションやtc-health-clientの主要設定ファイル/etc/trafficcontrol/tc-health-client.jsonの中を解析する
	cfg, err, helpflag := config.GetConfig()
	if err != nil {
		log.Errorln(err.Error())
		os.Exit(ConfigError)  // 166
	}

	// --helpオプションが指定されていたらそのまま終了する
	if helpflag { // user used --help option
		os.Exit(Success)     // 0
	}

	// TrafficMonitorへのポーリング間隔の秒数を表示する
	log.Infof("Polling interval: %v seconds\n", config.GetTMPollingInterval().Seconds())

	// parent情報としての重要な設定ファイル(parent.config, strategies.yaml)の読み込みを行う
	tmInfo, err := tmagent.NewParentInfo(cfg)
	if err != nil {
		log.Errorf("startup could not initialize parent info, check that trafficserver is running: %s\n", err.Error())
		os.Exit(RunTimeError)  // 167
	}

	// プロセスのPIDの取得
	pid := os.Getpid()

	// PID情報を/run/tc-health-client.pidに書き込む
	err = os.WriteFile(PidFile, []byte(strconv.Itoa(pid)), 0644)
	if err != nil {
		log.Errorf("could not write the process id to %s: %s", PidFile, err.Error())
		os.Exit(RunTimeError)  // 167
	}

	// バージョンとビルド時刻の情報を起動完了時に表示する
	log.Infof("startup complete, version: %s, built: %s\n", Version, BuildTimestamp)

	// メイン処理
	tmInfo.PollAndUpdateCacheStatus()
}
