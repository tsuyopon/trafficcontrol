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
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/apache/trafficcontrol/cache-config/t3cutil"

	"github.com/pborman/getopt/v2"
)

const AppName = "t3c-check-reload"

// Version is the application version.
// This is overwritten by the build with the current project version.
var Version = "0.4"

// GitRevision is the git revision the application was built from.
// This is overwritten by the build with the current project version.
var GitRevision = "nogit"

// t3c-check-reloadは変更されたファイルやインストールするプラグインなどの情報をjson形式で受け取り、trafficserverプロセスに対してreload, restart, 何もしないなどを決定します。
// t3c-check-reloadでは出力によって、reload, restart, 何もしないなどの処理を分岐している
func main() {
	// presumably calculated by by t3c-check-refs
	// TODO remove? The blueprint says t3c/ORT will no longer install packages

	version := getopt.BoolLong("version", 'V', "Print version information and exit.")
	help := getopt.BoolLong("help", 'h', "Print usage information and exit")
	getopt.Parse()

	if *help {
		fmt.Println(usageStr())
		os.Exit(0)
	} else if *version {
		fmt.Println(t3cutil.VersionStr(AppName, Version, GitRevision))
		os.Exit(0)
	}

	changedCfg := &ChangedCfg{}
	if err := json.NewDecoder(os.Stdin).Decode(changedCfg); err != nil {
		fmt.Println("Error reading json input", err)
	}

	// jsonファイルは下記の形式で指定される。changed_filesはカンマ区切りのリストで指定されているので下記で取得している
	//   {"changed_files":"<list of files>","installed_plugins":"<list of plugins>"}
	//    説明
	//          changed_files: 変更された設定ファイルパスのカンマで区切られたリスト、
	//          installed_plugins: インストールされたプラグインパッケージの神間で区切られたリスト
	// 
	changedConfigFiles := strings.Split(changedCfg.ChangedFiles, ",")
	changedConfigFiles = StrMap(changedConfigFiles, strings.TrimSpace)
	changedConfigFiles = StrRemoveIf(changedConfigFiles, StrIsEmpty)

	// ATS restart is needed if:
	// [x] 1. mode was badass
	// [x] 2. plugin.config or 50-ats.rules was changed
	// [ ] 3. package 'trafficserver' was installed

	// ATS reload is needed if:
	// [ ] 1. new SSL keys were installed AND ssl_multicert.config was changed
	// [ ] 2. any of the following were changed: url_sig*, uri_signing*, hdr_rw*, (plugin.config), (50-ats.rules),
	//        ssl/*.cer, ssl/*.key, anything else in /trafficserver,
	//

	// {"plugin.config", "50-ats.rules"}の2つのファイルがrangeで実行される
	for _, fileRequiringRestart := range configFilesRequiringRestart {

		// 下記では変更があったファイル一覧でイテレーション
		for _, changedPath := range changedConfigFiles {
			// もしファイルのsuffixが一致したら再起動させる
			if strings.HasSuffix(changedPath, fileRequiringRestart) {
				ExitRestart()
			}
		}
	}

	// 下記では変更があったファイル一覧でイテレーションする
	// 「ssl_multicert.config」や「hdr_rw_」、「url_sig_」、「uri_signing_」、「plugin.config」、「50-ats.rules」を含む場合にはrealodを実行する
	for _, path := range changedConfigFiles {

		// TODO add && ssl keys install
		// 変更されたファイルに「ssl_multicert.config」が含まれていたら、reloadさせる
		if strings.Contains(path, "ssl_multicert.config") /* && sslKeysInstalled */ {
			ExitReload()
		}

		// 変更されたファイルに「/trafficserver/」が含まれていたら、reloadさせる
		if strings.Contains(path, "/trafficserver/") {
			ExitReload()
		}

		// 変更されたファイルに「hdr_rw_」、「url_sig_」、「uri_signing_」、「plugin.config」、「50-ats.rules」を含む場合にはrealodを実行する
		if strings.Contains(path, "hdr_rw_") ||
			strings.Contains(path, "url_sig_") ||
			strings.Contains(path, "uri_signing_") ||
			strings.Contains(path, "plugin.config") ||
			strings.Contains(path, "50-ats.rules") {
			ExitReload()
		}
	}

	// 何もしない
	ExitNothing()
}

type ChangedCfg struct {
	ChangedFiles string `json:"changed_files"`
}

// ExitRestart returns the "needs restart" message and exits.
// ここでは出力するだけだが、呼び出し側ではこの戻り値で返される関数が実行される
func ExitRestart() {
	fmt.Fprintf(os.Stdout, t3cutil.ServiceNeedsRestart.String()+"\n")
	os.Exit(0)
}

// ExitReload returns the "needs reload" message and exits.
// ここでは出力するだけだが、呼び出し側ではこの戻り値で返される関数が実行される
func ExitReload() {
	fmt.Fprintf(os.Stdout, t3cutil.ServiceNeedsReload.String()+"\n")
	os.Exit(0)
}

// ExitNothing returns the "needs nothing" message and exits.
func ExitNothing() {
	os.Exit(0)
}

var configFilesRequiringRestart = []string{"plugin.config", "50-ats.rules"}

// StrMap applies the given function fn to all strings in strs.
func StrMap(strs []string, fn func(str string) string) []string {
	news := make([]string, 0, len(strs))
	for _, str := range strs {
		news = append(news, fn(str))
	}
	return news
}

// StrRemoveIf removes all strings in strs for which fn returns true.
func StrRemoveIf(strs []string, fn func(str string) bool) []string {
	news := []string{}
	for _, str := range strs {
		if fn(str) {
			continue
		}
		news = append(news, str)
	}
	return news
}

// StrIsEmpty returns whether str == "". Helper function for composing with other functions.
func StrIsEmpty(str string) bool { return str == "" }

func usageStr() string {
	return `usage: t3c-check-reload [--help]
Accepts json data from stdin in in the following format:
{"changed_files":"<comma separated list of files>"}
`
}
