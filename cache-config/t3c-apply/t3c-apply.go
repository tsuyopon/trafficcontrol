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
	"time"

	"github.com/apache/trafficcontrol/cache-config/t3c-apply/config"
	"github.com/apache/trafficcontrol/cache-config/t3c-apply/torequest"
	"github.com/apache/trafficcontrol/cache-config/t3c-apply/util"
	"github.com/apache/trafficcontrol/cache-config/t3cutil"
	"github.com/apache/trafficcontrol/lib/go-log"
	tcutil "github.com/apache/trafficcontrol/lib/go-util"
)

// Version is the application version.
// This is overwritten by the build with the current project version.
var Version = "0.4"

// GitRevision is the git revision the application was built from.
// This is overwritten by the build with the current project version.
var GitRevision = "nogit"

const (
	ExitCodeSuccess           = 0
	ExitCodeAlreadyRunning    = 132
	ExitCodeConfigFilesError  = 133
	ExitCodeConfigError       = 134
	ExitCodeGeneralFailure    = 135
	ExitCodePackagingError    = 136
	ExitCodeRevalidationError = 137
	ExitCodeServicesError     = 138
	ExitCodeSyncDSError       = 139
	ExitCodeUserCheckError    = 140
)

func runSysctl(cfg config.Cfg) {

	// report-onlyオプションが指定された場合には何もしない
	if cfg.ReportOnly {  //  --report-only=true
		return
	}

	if cfg.ServiceAction == t3cutil.ApplyServiceActionFlagRestart {  // --service-action=restart
		_, rc, err := util.ExecCommand("/usr/sbin/sysctl", "-p")
		if err != nil {
			log.Errorln("sysctl -p failed")
		} else if rc == 0 {
			log.Debugf("sysctl -p ran succesfully.")
		}
	}
}

const LockFilePath = "/var/run/t3c.lock"
const LockFileRetryInterval = time.Second
const LockFileRetryTimeout = time.Minute

const FailureExitMsg = `CRITICAL FAILURE, ABORTING`
const PostConfigFailureExitMsg = `CRITICAL FAILURE AFTER SETTING CONFIG, ABORTING`
const SuccessExitMsg = `SUCCESS`

func main() {
	os.Exit(LogPanic(Main))
}

// Main is the main function of t3c-apply.
// This is a separate function so defer statements behave as-expected.
// DO NOT call os.Exit within this function; return the code instead.
// Returns the application exit code.
// t3c-applyは「t3c apply」コマンドから呼ばれます。
func Main() int {

	var syncdsUpdate torequest.UpdateStatus
	var lock util.FileLock

	// t3c-applyコマンドに指定されたオプションの解析処理を行います
	cfg, err := config.GetCfg(Version, GitRevision)
	if err != nil {
		fmt.Println(err)
		fmt.Println(FailureExitMsg)
		return ExitCodeConfigError
	} else if cfg == (config.Cfg{}) { // user used the --help option
		return ExitCodeSuccess
	}

	// /var/run/t3c.lockがあるかどうかでこのプロセスがロックされているかをチェックします。
	log.Infoln("Trying to acquire app lock")
	for lockStart := time.Now(); !lock.GetLock(LockFilePath); {

		if time.Since(lockStart) > LockFileRetryTimeout {
			log.Errorf("Failed to get app lock after %v seconds, another instance is running, exiting without running\n", int(LockFileRetryTimeout/time.Second))
			log.Infoln(FailureExitMsg)
			return ExitCodeAlreadyRunning
		}

		// 一定時間sleepする
		time.Sleep(LockFileRetryInterval)
	}
	log.Infoln("Acquired app lock")

	// オプションに--git=yesが指定されている場合
	if cfg.UseGit == config.UseGitYes {
		// gitレポジトリがなければgit initにより生成する
		err := util.EnsureConfigDirIsGitRepo(cfg)
		if err != nil {
			log.Errorln("Ensuring config directory '" + cfg.TsConfigDir + "' is a git repo - config may not be a git repo! " + err.Error())
		} else {
			log.Infoln("Successfully ensured ATS config directory '" + cfg.TsConfigDir + "' is a git repo")
		}
	} else {
		log.Infoln("UseGit not 'yes', not creating git repo")
	}

	// オプションに --git=yes または --git=auto が指定されている場合
	if cfg.UseGit == config.UseGitYes || cfg.UseGit == config.UseGitAuto {
		// commit anything someone else changed when we weren't looking,
		// with a keyword indicating it wasn't our change
		// このプログラムではない誰かが変更したものについては、このプログラムのcommitでないコメントを指定してcommitする
		if err := util.MakeGitCommitAll(cfg, util.GitChangeNotSelf, true); err != nil {
			log.Errorln("git committing existing changes, dir '" + cfg.TsConfigDir + "': " + err.Error())
		}
	}

	// オブジェクトの生成を行う
	trops := torequest.NewTrafficOpsReq(cfg)

	// if doing os checks, insure there is a 'systemctl' or 'service' and 'chkconfig' commands.
	//
	// --skip-os-check=false かつ /bin/shの実行結果がSystemDやSystemVいずれでもないと判断した場合にはエラーログだけ出力させて処理を続行させる
	if !cfg.SkipOSCheck && cfg.SvcManagement == config.Unknown {
		log.Errorln("OS checks are enabled and unable to find any know service management tools.")
	}

	// create and clean the config.TmpBase (/tmp/ort)
	// 下記では結局、MkDirして、その後CleanTmpDirを実行します。
	// /tmp/trafficcontrol-cache-configを適切な権限・オーナーで生成します。
	if !util.MkDir(config.TmpBase, cfg) {
		log.Errorln("mkdir TmpBase '" + config.TmpBase + "' failed, cannot continue")
		log.Infoln(FailureExitMsg)
		return ExitCodeGeneralFailure
	} else if !util.CleanTmpDir(cfg) {
		log.Errorln("CleanTmpDir failed, cannot continue")
		log.Infoln(FailureExitMsg)
		return ExitCodeGeneralFailure
	}

	log.Infoln(time.Now().Format(time.RFC3339))

	// 実行プロセスがrootユーザーであることのチェックを行う(restartやreloadが必要となるため)
	if !util.CheckUser(cfg) {
		lock.Unlock()
		return ExitCodeUserCheckError
	}

	// if running in Revalidate mode, check to see if it's
	// necessary to continue
	// filesにrevalモードが指定されている場合の処理
	if cfg.Files == t3cutil.ApplyFilesFlagReval { // --files=revalの場合

		// TrafficOpsから変更後のステータス(--get-data=update-status)と変更前の現状ステータス(--get-data=statuses)をそれぞれ取得して、
		// ステータスに変更があれば、/var/lib/trafficcontrol-cache-config/status/<status> のファイルを作成する(古いステータスファイルは削除する)
		syncdsUpdate, err = trops.CheckRevalidateState(false)

		if err != nil {
			log.Errorln("Checking revalidate state: " + err.Error())
			return GitCommitAndExit(ExitCodeRevalidationError, FailureExitMsg, cfg)
		}

		if syncdsUpdate == torequest.UpdateTropsNotNeeded {
			log.Infoln("Checking revalidate state: returned UpdateTropsNotNeeded")
			return GitCommitAndExit(ExitCodeRevalidationError, SuccessExitMsg, cfg)
		}

	} else {  // --files=allの場合

		// 下記ではTrafficOpsからステータスを取得した後に、その結果がPendingステータスの場合に再度ステータスを取得するために(t3c-request)が実行される
		// DSはDelivery Serviceである。
		syncdsUpdate, err = trops.CheckSyncDSState()
		if err != nil {
			log.Errorln("Checking syncds state: " + err.Error())
			return GitCommitAndExit(ExitCodeSyncDSError, FailureExitMsg, cfg)
		}

		// --ignore-update-flag=false --files=all + UpdateTropsNotNeeded の場合
		if !cfg.IgnoreUpdateFlag && cfg.Files == t3cutil.ApplyFilesFlagAll && syncdsUpdate == torequest.UpdateTropsNotNeeded {

			// If touching remap.config fails, we want to still try to restart services
			// But log a critical-post-config-failure, which needs logged right before exit.
			postConfigFail := false

			// check for maxmind db updates even if we have no other updates
			// オプションでmaxmind-locationのURLが指定されている場合には下記で処理が実行される
			if CheckMaxmindUpdate(cfg) {

				// remap.configをtouchして更新しておく
				// We updated the db so we should touch and reload
				trops.RemapConfigReload = true
				path := cfg.TsConfigDir + "/remap.config"
				_, rc, err := util.ExecCommand("/usr/bin/touch", path)
				if err != nil {
					log.Errorf("failed to update the remap.config for reloading: %s\n", err.Error())
					postConfigFail = true
				} else if rc == 0 {
					log.Infoln("updated the remap.config for reloading.")
				}

				// TBD: このケースはUpdateTropsNotNeededで更新不要なのになぜ再起動を行う必要があるのか? -> 指定されたオプションで再起動を常にしたいような場合なのか?
				// trafficserverの起動をおこなっておく
				if err := trops.StartServices(&syncdsUpdate); err != nil {
					log.Errorln("failed to start services: " + err.Error())
					return GitCommitAndExit(ExitCodeServicesError, PostConfigFailureExitMsg, cfg)
				}

			}
			finalMsg := SuccessExitMsg
			if postConfigFail {
				finalMsg = PostConfigFailureExitMsg
			}

			// このケースのコードパスの場合にはここでreturnしてmainが正常終了する
			return GitCommitAndExit(ExitCodeSuccess, finalMsg, cfg)
		}
	}

	if cfg.Files != t3cutil.ApplyFilesFlagAll { // --files=all 以外である場合
		// make sure we got the data necessary to check packages
		log.Infoln("======== Didn't get all files, no package processing needed or possible ========")
	} else {
		log.Infoln("======== Start processing packages  ========")

		// TrafficOpsからサーバにインストールが必要なリストを取得して、パッケージのyum remove, yum installを実施する。
		err = trops.ProcessPackages()
		if err != nil {
			log.Errorf("Error processing packages: %s\n", err)
			return GitCommitAndExit(ExitCodePackagingError, FailureExitMsg, cfg)
		}

		// check and make sure packages are enabled for startup
		// t3c-request --get-data=chkconfigで取得した値を元にして必要なサービスを有効にします。
		err = trops.CheckSystemServices()
		if err != nil {
			log.Errorf("Error verifying system services: %s\n", err.Error())
			return GitCommitAndExit(ExitCodeServicesError, FailureExitMsg, cfg)
		}
	}

	log.Debugf("Preparing to fetch the config files for %s, files: %s, syncdsUpdate: %s\n", cfg.CacheHostName, cfg.Files, syncdsUpdate)

	// TBD: CheckSyncDSState -> GetConfigFileList経由でgenerate()が実行されているが、それと何が違うのか? 2度呼ばれることにならないのか。
	// TrafficOpsからの設定ファイルの取得と生成はここで行われている。t3c-generateとファイル情報をオブジェクトにマッピングしている(その情報はその後のtrops.ProcessConfigFiles()で使われる)
	err = trops.GetConfigFileList()
	if err != nil {
		log.Errorf("Getting config file list: %s\n", err)
		return GitCommitAndExit(ExitCodeConfigFilesError, FailureExitMsg, cfg)
	}

	// 手前のtrops.GetConfigFileList()で取得したファイルオブジェクトに対して処理を実施する
	syncdsUpdate, err = trops.ProcessConfigFiles()
	if err != nil {
		log.Errorf("Error while processing config files: %s\n", err.Error())
	}

	// check for maxmind db updates
	// If we've updated also reload remap to reload the plugin and pick up the new database
	// --maxmind-locationオプションにURLが指定されている場合にフラグが変更される
	if CheckMaxmindUpdate(cfg) {        // CheckMaxmindUpdate()の中で対象URLにヘッドリクエストして200ならcurl取得、gzip展開、保存をし、304ならばローカルファイルを更新する。
		trops.RemapConfigReload = true  // このすぐ後にこのフラグが判定に利用される
	}

	// trops.RemapConfigReloadのフラグはこの上の直前でセットされる
	if trops.RemapConfigReload == true {
		// remap.configのパス情報を取得して、そのパスに対してtouchして時刻を更新している。
		cfg, ok := trops.GetConfigFile("remap.config")
		_, rc, err := util.ExecCommand("/usr/bin/touch", cfg.Path)
		if err != nil {
			log.Errorf("failed to update the remap.config for reloading: %s\n", err.Error())
		} else if rc == 0 && ok == true {
			// 正常に終了した場合
			log.Infoln("updated the remap.config for reloading.")
		}
	}

	// --service-action=restart オプションやt3c-check-reloadの実行結果によってtrafficserverを再起動・再読み込み・何もしない・不正かを判断し、
	// それに従ってtrafficserverを再起動します
	if err := trops.StartServices(&syncdsUpdate); err != nil {
		log.Errorln("failed to start services: " + err.Error())
		return GitCommitAndExit(ExitCodeServicesError, PostConfigFailureExitMsg, cfg)
	}

	// start 'teakd' if installed.
	// このパッケージがtrafficcontrolで利用されている形跡を見つけることができない。
	if trops.IsPackageInstalled("teakd") {
		svcStatus, pid, err := util.GetServiceStatus("teakd")
		if err != nil {
			log.Errorf("not starting 'teakd', error getting 'teakd' run status: %s\n", err)
		} else if svcStatus == util.SvcNotRunning {
			running, err := util.ServiceStart("teakd", "start")
			if err != nil {
				log.Errorf("'teakd' was not started: %s\n", err)
			} else if running {
				log.Infoln("service 'teakd' started.")
			} else if svcStatus == util.SvcRunning {
				log.Infof("service 'teakd' was already running, pid: %v\n", pid)
			}
		}
	}

	// reload sysctl
	if trops.SysCtlReload == true {
		// --service-action=restart が指定されている場合には、「sysctl -p」が実行される
		runSysctl(cfg)
	}

	// r.configFileWarningsに登録されている内容があればここで表示する ( GetConfigFileList()関数内のgenerate()後にこの値が詰められそう)
	trops.PrintWarnings()

	// TrafficOps APIに対してserverStatusの更新処理を行う
	if err := trops.UpdateTrafficOps(&syncdsUpdate); err != nil {
		log.Errorf("failed to update Traffic Ops: %s\n", err.Error())
	}

	// ローカルにあるgitにcommitして成功として終了する。
	return GitCommitAndExit(ExitCodeSuccess, SuccessExitMsg, cfg)
}

func LogPanic(f func() int) (exitCode int) {
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("panic: (err: %v) stacktrace:\n%s\n", err, tcutil.Stacktrace())
			log.Infoln(FailureExitMsg)
			exitCode = ExitCodeGeneralFailure
			return
		}
	}()
	return f()
}

// GitCommitAndExit attempts to git commit all changes, and logs any error.
// It then logs exitMsg at the Info level, and returns exitCode.
// This is a helper function, to reduce the duplicated commit-log-return into a single line.
// サーバ内部のローカルのgitにコミットする(これによって履歴として確認できるようになる)
func GitCommitAndExit(exitCode int, exitMsg string, cfg config.Cfg) int {
	success := exitCode == ExitCodeSuccess
	if cfg.UseGit == config.UseGitYes || cfg.UseGit == config.UseGitAuto {
		if err := util.MakeGitCommitAll(cfg, util.GitChangeIsSelf, success); err != nil {
			log.Errorln("git committing existing changes, dir '" + cfg.TsConfigDir + "': " + err.Error())
		}
	}
	log.Infoln(exitMsg)
	return exitCode
}

// CheckMaxmindUpdate will (if a url is set) check for a db on disk.
// If it exists, issue an IMS to determine if it needs to update the db.
// If no file or if an update is needed to be done it is downloaded and unpacked.
func CheckMaxmindUpdate(cfg config.Cfg) bool {
	// Check if we have a URL for a maxmind db
	// If we do, test if the file exists, do IMS based on disk time
	// and download and unpack as needed
	result := false
	// --maxmind-locationオプションにURLが指定されている場合。このオプションにはgzipで圧縮されたmaxminddbへのURLのパスが指定される。そのdbはtrafficserverのetcにインストールされる。
	if cfg.MaxMindLocation != "" {
		// Check if the maxmind db needs to be updated before reload
		result = util.UpdateMaxmind(cfg)
		if result {
			log.Infoln("maxmind database was updated from " + cfg.MaxMindLocation)
		} else {
			log.Infoln("maxmind database not updated. Either not needed or curl/gunzip failure")
		}
	} else {
		log.Infoln(("maxmindlocation is empty, not checking for DB update"))
	}

	return result
}
