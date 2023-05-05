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
	"fmt"
	"os"
	"os/exec"
	"syscall" // TODO change to x/unix ?

	"github.com/apache/trafficcontrol/cache-config/t3cutil"
	"github.com/apache/trafficcontrol/lib/go-log"

	"github.com/pborman/getopt/v2"
)

const AppName = "t3c-check"

// Version is the application version.
// This is overwritten by the build with the current project version.
var Version = "0.4"

// GitRevision is the git revision the application was built from.
// This is overwritten by the build with the current project version.
var GitRevision = "nogit"

var commands = map[string]struct{}{
	"apply":      struct{}{},
	"check":      struct{}{},
	"diff":       struct{}{},
	"generate":   struct{}{},
	"preprocess": struct{}{},
	"request":    struct{}{},
	"update":     struct{}{},
}

const ExitCodeSuccess = 0
const ExitCodeNoCommand = 1
const ExitCodeUnknownCommand = 2
const ExitCodeCommandErr = 3
const ExitCodeExeErr = 4
const ExitCodeCommandLookupErr = 5

// t3cで始まる全てのコマンドのラッパーになります。
func main() {

	flagHelp := getopt.BoolLong("help", 'h', "Print usage information and exit")
	flagVersion := getopt.BoolLong("version", 'V', "Print version information and exit.")
	getopt.Parse()

	// 5つのログレベルで初期化します。最初は全てStderrとして登録されます。
	log.Init(os.Stderr, os.Stderr, os.Stderr, os.Stderr, os.Stderr)

	// -hや-Vの場合にはメッセージを表示してプログラムを終了します。
	if *flagHelp {
		log.Errorln(usageStr())
		os.Exit(ExitCodeSuccess)
	} else if *flagVersion {
		fmt.Println(t3cutil.VersionStr(AppName, Version, GitRevision))
		os.Exit(ExitCodeSuccess)
	}

	// 引数が2つ未満の場合には指定方法が間違っているので処理を終了します
	if len(os.Args) < 2 {
		log.Errorf("no command\n\n%s", usageStr())
		os.Exit(ExitCodeNoCommand)
	}

	// t3cではコマンド引数が下記のいずれかでないとエラーになります。
	//   apply, check, diff, generate, preprocess, request, update
	cmd := os.Args[1]
	if _, ok := commands[cmd]; !ok {
		log.Errorf("unknown command\n%s", usageStr())
		os.Exit(ExitCodeUnknownCommand)
	}

	// t3c-xxxxコマンドの文字列名称を生成します。例えば、「t3c apply 〜」ならば「t3c-apply」がここで生成されます。
	app := "t3c-" + cmd

	// 指定されたプログラム名のパスが実在するかどうかを探索します。
	appPath, err := exec.LookPath(app)
	if err != nil {
		log.Errorf("error finding path to '%s': %s\n", app, err.Error())
		os.Exit(ExitCodeCommandLookupErr)
	}

	// 2番目以降の引数はそのまま指定させる
	args := append([]string{app}, os.Args[2:]...)

	// 指定された環境変数を引き継がせます
	env := os.Environ()

	// 指定された引数と環境変数を指定して、
	if err := syscall.Exec(appPath, args, env); err != nil {
		log.Errorf("error executing sub-command '%s': %s\n", appPath, err.Error())
		os.Exit(ExitCodeCommandErr)
	}
}

func usageStr() string {
	return `usage: t3c [--help]
       <command> [<args>]

For the arguments of a command, see 't3c <command> --help'.

These are the available commands:

  apply      generate and apply configuration

  check      check that new config can be applied
  diff       diff config files, with logic like ignoring comments
  generate   generate configuration from Traffic Ops data
  preprocess preprocess generated config files
  request    request Traffic Ops data
  update     update a cache's queue and reval status in Traffic Ops
`
}
