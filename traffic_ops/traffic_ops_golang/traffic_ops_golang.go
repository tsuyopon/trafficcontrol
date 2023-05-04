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
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/about"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/auth"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/config"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/plugin"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/routing"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/server"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/trafficvault"
	_ "github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/trafficvault/backends" // init traffic vault backends
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/trafficvault/backends/disabled"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/trafficvault/backends/riaksvc"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"golang.org/x/sys/unix"
)

// set the version at build time: `go build -X "main.version=..."`
var version = "development"

func init() {
	about.SetAbout(version)
}

func main() {

	// 指定されたオプションの取得処理
	showVersion := flag.Bool("version", false, "Show version and exit")
	showPlugins := flag.Bool("plugins", false, "Show the list of plugins and exit")
	showRoutes := flag.Bool("api-routes", false, "Show the list of API routes and exit")
	configFileName := flag.String("cfg", "", "The config file path")
	dbConfigFileName := flag.String("dbcfg", "", "The db config file path")
	riakConfigFileName := flag.String("riakcfg", "", "The riak config file path (DEPRECATED: use traffic_vault_backend = riak and traffic_vault_config in cdn.conf instead)")
	backendConfigFileName := flag.String("backendcfg", "", "The backend config file path")
	flag.Parse()

	// --versionが指定されていた場合
	if *showVersion {
		fmt.Println(about.About.RPMVersion)
		os.Exit(0)
	}

	// --pluginsが指定されていた場合、プラグインリストを表示して終了
	if *showPlugins {
		fmt.Println(strings.Join(plugin.List(), "\n"))
		os.Exit(0)
	}

	// --api-routesが指定されていた場合、対象のAPI一覧を表示して終了
	if *showRoutes {
		fake := routing.ServerData{Config: config.NewFakeConfig()}

		// APIで定義されている全エンドポイントの情報(IDなど含む)を取得する
		routes, _, _ := routing.Routes(fake)

		// cdn.confが指定されている場合と指定されていない場合
		if len(*configFileName) != 0 {

			// cdn.confの情報を読み込む
			cfg, err := config.LoadCdnConfig(*configFileName)
			if err != nil {
				fmt.Printf("Loading cdn config from '%s': %v", *configFileName, err)
				os.Exit(1)
			}

			// 設定ファイル中のdisabled_routesにIDが指定されていたら、disableされたエンドポイントであることを示す。
			// 指定されたIDのエンドポイントは無効化できることを表します。
			disabledRoutes := routing.GetRouteIDMap(cfg.DisabledRoutes)

			// 提供されているAPIエンドポイント毎にイテレーションする。内部ではdisableRoutesに該当するかをチェックして、該当していればその情報を「is_disabled=true」のように出力する
			for _, r := range routes {
				_, isDisabled := disabledRoutes[r.ID]

				// rによってid, method, versio, pathなども出力される。これはrouting/routing.goに「func (r Route) String() string」 が出力フォーマットとして定義されているから。
				// 例:  id=541357729077	method=GET	version=4.0	path=OC/FCI/advertisement/?$
				fmt.Printf("%s\tis_disabled=%t\n", r, isDisabled) 
			}

		} else {
			//  --cfgが指定されていない場合には、APIのエンドポイントの情報を出力する
			for _, r := range routes {
				fmt.Printf("%s\n", r)
			}
		}
		os.Exit(0)
	}

	// 引数が2つ未満なければエラー。つまり2つは必須
	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	// 引数に--cfg, --dbcfgの設定ファイルを指定する
	// 初期化に成功すると blockStart = false が入る。失敗すると blockStart = true となる
	cfg, errsToLog, blockStart := config.LoadConfig(*configFileName, *dbConfigFileName, version)
	for _, err := range errsToLog {
		fmt.Fprintf(os.Stderr, "Loading Config: %v\n", err)
	}

	// config.LoadConfigでの初期化に失敗したらエラーとさせる
	if blockStart {
		os.Exit(1)
	}

	// ログオブジェクトの初期化を行います
	if err := log.InitCfg(cfg); err != nil {
		fmt.Printf("Error initializing loggers: %v\n", err)
		for _, err := range errsToLog {
			fmt.Println(err)
		}
		os.Exit(1)
	}

	// 設定読み込み時に何か警告があれば出力しておきう
	for _, err := range errsToLog {
		log.Warnln(err)
	}

	// 主要な設定情報を出力するだけ
	logConfig(cfg)

	// パスワードのブラックリストが取得できなければエラー
	err := auth.LoadPasswordBlacklist("app/conf/invalid_passwords.txt")
	if err != nil {
		log.Errorf("loading password blacklist: %v\n", err)
		os.Exit(1)
	}

	// SSLが必要かどうかを設定値から判定する
	sslStr := "require"
	if !cfg.DB.SSL {
		sslStr = "disable"
	}

	// PostgreSQLへの接続を行う
	db, err := sqlx.Open("postgres", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&fallback_application_name=trafficops", cfg.DB.User, cfg.DB.Password, cfg.DB.Hostname, cfg.DB.Port, cfg.DB.DBName, sslStr))
	if err != nil {
		log.Errorf("opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// DBへの設定を行う
	db.SetMaxOpenConns(cfg.MaxDBConnections)     // max_db_connections設定
	db.SetMaxIdleConns(cfg.DBMaxIdleConnections) // db_max_idle_connections設定
	db.SetConnMaxLifetime(time.Duration(cfg.DBConnMaxLifetimeSeconds) * time.Second)  // db_conn_max_lifetime_seconds設定

	// 定期的にユーザー情報+ 権限情報をキャッシュするためにgoroutineを起動します
	auth.InitUsersCache(time.Duration(cfg.UserCacheRefreshIntervalSec)*time.Second, db.DB, time.Duration(cfg.DBQueryTimeoutSeconds)*time.Second)

	// 定期的にサーバのステータス情報を取得して、更新後のステータスとして保持しておくgoroutineを起動する
	server.InitServerUpdateStatusCache(time.Duration(cfg.ServerUpdateStatusCacheRefreshIntervalSec)*time.Second, db.DB, time.Duration(cfg.DBQueryTimeoutSeconds)*time.Second)

	// TrafficVaultに関する設定の取得を行う
	trafficVault := setupTrafficVault(*riakConfigFileName, &cfg)

	// cdn.confに指定された有効なプラグイン情報のオブジェクト情報を取得する。(cdn.confに指定された「plugin」、「plugin_config」の設定を参照する)
	plugins := plugin.Get(cfg)

	// 設定: profiling_enabledを取得する
	profiling := cfg.ProfilingEnabled

	// HTTPサーバ「localhost:6060」として「/db-stats」、「/memory-stats」のプロファイリング用エンドポイントを起動する
	pprofMux := http.DefaultServeMux
	http.DefaultServeMux = http.NewServeMux() // this is so we don't serve pprof over 443.
	pprofMux.Handle("/db-stats", routing.DBStatsHandler(db))
	pprofMux.Handle("/memory-stats", routing.MemoryStatsHandler())
	go func() {
		// デバッグ用HTTPサーバ
		debugServer := http.Server{
			Addr:    "localhost:6060",
			Handler: pprofMux,
		}
		log.Errorln(debugServer.ListenAndServe())
	}()

	var backendConfig config.BackendConfig

	// --backendcfgでファイルが指定された場合 (一般名称: backends.conf)
	// backends.confファイルによって特定のエンドポイントパスや特定のメソッドにリクエストがきた場合には、バックエンドサーバへの振り分けを行うことができる。API認可も制御できる。
	if *backendConfigFileName != "" {
		backendConfig, err = config.LoadBackendConfig(*backendConfigFileName)
		routing.SetBackendConfig(backendConfig)
		if err != nil {
			log.Errorf("error loading backend config: %v", err)
		}
	}

	// APIエンドポイントへの登録に必要なオブジェクトを生成する
	mux := http.NewServeMux()
	d := routing.ServerData{DB: db, Config: cfg, Profiling: &profiling, Plugins: plugins, TrafficVault: trafficVault, Mux: mux}

	// (重要) **メイン処理** TrafficOps APIエンドポイントの登録は下記で行います。APIエンドポイント毎のハンドラマッピングも下記で定義されています。
	if err := routing.RegisterRoutes(d); err != nil {
		log.Errorf("registering routes: %v\n", err)
		os.Exit(1)
	}

	// cfg.PluginSharedConfig=plugin_shared_config
	plugins.OnStartup(plugin.StartupData{Data: plugin.Data{SharedCfg: cfg.PluginSharedConfig, AppCfg: cfg}})

	// ポート番号のログ出力
	log.Infof("Listening on " + cfg.Port)

	// HTTPサーバオブジェクトの設定を行う (その後のgoroutineからこのHTTPサーバは起動させる)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		TLSConfig:         cfg.TLSConfig,
		ReadTimeout:       time.Duration(cfg.ReadTimeout) * time.Second,
		ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeout) * time.Second,
		WriteTimeout:      time.Duration(cfg.WriteTimeout) * time.Second,
		IdleTimeout:       time.Duration(cfg.IdleTimeout) * time.Second,
		ErrorLog:          log.Error,
	}

	// TLS設定がなければ、TLSオブジェクトの空設定を埋め込んでおく
	if httpServer.TLSConfig == nil {
		httpServer.TLSConfig = &tls.Config{}
	}

	// Deprecated in 5.0
	// 接続時の検証をskipするかどうかを設定により決定する
	httpServer.TLSConfig.InsecureSkipVerify = cfg.Insecure
	// end deprecated block

	// goroutineによりHTTPSサーバを起動する
	go func() {

		// TLSの秘密鍵のパスを取得する
		if cfg.KeyPath == "" {
			log.Errorf("key cannot be blank in %s", cfg.ConfigHypnotoad.Listen)
			os.Exit(1)
		}

		// TLS用のX509証明書のパスを取得する
		if cfg.CertPath == "" {
			log.Errorf("cert cannot be blank in %s", cfg.ConfigHypnotoad.Listen)
			os.Exit(1)
		}

		// TLS証明書のパスのファイルをopenする
		if file, err := os.Open(cfg.CertPath); err != nil {
			log.Errorf("cannot open %s for read: %s", cfg.CertPath, err.Error())
			os.Exit(1)
		} else {
			file.Close()
		}

		// TLSの秘密鍵のパスのファイルをopenする
		if file, err := os.Open(cfg.KeyPath); err != nil {
			log.Errorf("cannot open %s for read: %s", cfg.KeyPath, err.Error())
			os.Exit(1)
		} else {
			file.Close()
		}

		// HTTPSサーバを起動する
		httpServer.Handler = mux
		if err := httpServer.ListenAndServeTLS(cfg.CertPath, cfg.KeyPath); err != nil {
			log.Errorf("stopping server: %v\n", err)
			os.Exit(1)
		}

	}()  // goroutineここまで

	// profilingLocationとcfg.LogLocationErrorのバリデーション処理を行う
	profilingLocation, err := getProcessedProfilingLocation(cfg.ProfilingLocation, cfg.LogLocationError)  // 設定: profiling_location, log_location_error
	if err != nil {
		log.Errorln("unable to determine profiling location: " + err.Error())
	}

	// プロファイリング情報をログに出力する
	log.Infof("profiling location: %s\n", profilingLocation)
	log.Infof("profiling enabled set to %t\n", profiling)

	// `profiling_enabled=true`の場合、CPUプロファイリングの計測処理が行われる(特定のファイルに書かれる)
	if profiling {
		continuousProfile(&profiling, &profilingLocation, cfg.Version)
	}

	// 次のsignalReload()に引き渡すための無名関数の定義を行う
	reloadProfilingAndBackendConfig := func() {
		setNewProfilingInfo(*configFileName, &profiling, &profilingLocation, cfg.Version)

		// 指定されたbackend設定ファイルを構造体に変換して、セットする
		backendConfig, err = getNewBackendConfig(backendConfigFileName)
		if err != nil {
			log.Errorf("could not reload backend config: %v", err)
		} else {
			routing.SetBackendConfig(backendConfig)
		}
	}

	// SIGHUPを受信したらreloadProfilingAndBackendConfigの無名関数が実行される様にする
	signalReloader(unix.SIGHUP, reloadProfilingAndBackendConfig)
}

func setupTrafficVault(riakConfigFileName string, cfg *config.Config) trafficvault.TrafficVault {

	var err error
	trafficVaultConfigBytes := []byte{}
	trafficVaultBackend := ""

	// --riakcfgが指定されていれば、読み込む
	if len(riakConfigFileName) > 0 {
		// use legacy riak config if given
		// --riakcfgjはdeprecatedなのでその旨を出力する
		log.Warnln("using deprecated --riakcfg flag, use traffic_vault_backend = riak and traffic_vault_config in cdn.conf instead")
		trafficVaultConfigBytes, err = ioutil.ReadFile(riakConfigFileName)

		// --riakcfgで指定されたファイルが読み込みできなければエラーとする
		if err != nil {
			log.Errorf("reading riak conf '%s': %s", riakConfigFileName, err.Error())
			os.Exit(1)
		}
		cfg.TrafficVaultEnabled = true
		trafficVaultBackend = riaksvc.RiakBackendName
	}

	// 設定ファイルにtraffic_vault_backendが指定されていれば
	if len(cfg.TrafficVaultBackend) > 0 {

		// traffic_vault_backendが指定されていないか空の場合
		if len(cfg.TrafficVaultConfig) == 0 {
			log.Errorln("traffic_vault_backend is non-empty but traffic_vault_config is empty")
			os.Exit(1)
		}

		cfg.TrafficVaultEnabled = true
		// traffic_vault_config should override legacy riak config if both are used
		trafficVaultConfigBytes = cfg.TrafficVaultConfig  // traffic_vault_config設定(postgresqlに関するhost, port, pwなど)
		trafficVaultBackend = cfg.TrafficVaultBackend     // traffic_vault_backend設定("postgres", "riak"などが設定される)
	}

	// traffic_vault_backend == "riak" でかつ RiakPortが指定されている場合。つまり、Riakに関連する処理
	if trafficVaultBackend == riaksvc.RiakBackendName && cfg.RiakPort != nil {
		// inject riak_port into traffic_vault_config.port if unset there
		log.Warnln("using deprecated field 'riak_port', use 'port' field in traffic_vault_config instead")
		tmp := make(map[string]interface{})
		err := json.Unmarshal(trafficVaultConfigBytes, &tmp)

		if err != nil {
			log.Errorf("failed to unmarshal riak config: %s", err.Error())
			os.Exit(1)
		}

		if _, ok := tmp["port"]; !ok {
			tmp["port"] = *cfg.RiakPort
		}

		trafficVaultConfigBytes, err = json.Marshal(tmp)
		if err != nil {
			log.Errorf("failed to marshal riak config: %s", err.Error())
			os.Exit(1)
		}
	}

	if cfg.TrafficVaultEnabled {
		trafficVault, err := trafficvault.GetBackend(trafficVaultBackend, trafficVaultConfigBytes)
		if err != nil {
			log.Errorf("failed to get Traffic Vault backend '%s': %s", cfg.TrafficVaultBackend, err.Error())
			os.Exit(1)
		}
		return trafficVault
	}
	return &disabled.Disabled{}
}

func getNewBackendConfig(backendConfigFileName *string) (config.BackendConfig, error) {

	// 設定ファイルがnilならばエラー
	if backendConfigFileName == nil {
		return config.BackendConfig{}, errors.New("no backend config filename")
	}

	log.Infof("setting new backend config to %s", *backendConfigFileName)

	// 設定ファイルをunmarshalする
	backendConfig, err := config.LoadBackendConfig(*backendConfigFileName)
	if err != nil {
		log.Errorf("error reloading config: %v", err)
		return backendConfig, err
	}

	return backendConfig, nil
}

func setNewProfilingInfo(configFileName string, currentProfilingEnabled *bool, currentProfilingLocation *string, version string) {

	newProfilingEnabled, newProfilingLocation, err := reloadProfilingInfo(configFileName)
	if err != nil {
		log.Errorln("reloading config: ", err.Error())
		return
	}

	if newProfilingLocation != "" && *currentProfilingLocation != newProfilingLocation {
		*currentProfilingLocation = newProfilingLocation
		log.Infof("profiling location set to: %s\n", *currentProfilingLocation)
	}

	if *currentProfilingEnabled != newProfilingEnabled {
		log.Infof("profiling enabled set to %t\n", newProfilingEnabled)
		log.Infof("profiling location set to: %s\n", *currentProfilingLocation)
		*currentProfilingEnabled = newProfilingEnabled
		if *currentProfilingEnabled {
			continuousProfile(currentProfilingEnabled, currentProfilingLocation, version)
		}
	}

}

// errorLogLocationの値をバリデーションし、rawProfilingLocationのパスディレクトリが存在することを検証する。
func getProcessedProfilingLocation(rawProfilingLocation string, errorLogLocation string) (string, error) {
	profilingLocation := os.TempDir()

	// errorLogLocationに格納されている値のバリデーションを行う
	if errorLogLocation != "" && errorLogLocation != log.LogLocationNull && errorLogLocation != log.LogLocationStderr && errorLogLocation != log.LogLocationStdout {
		errorDir := filepath.Dir(errorLogLocation)
		if _, err := os.Stat(errorDir); err == nil {
			profilingLocation = errorDir
		}
	}

	// 指定されたrawProfilingLocationを元にパスを取得する。rawProfilingLocationの値が空ならば独自で生成する
	profilingLocation = filepath.Join(profilingLocation, "profiling")
	if rawProfilingLocation != "" {
		profilingLocation = rawProfilingLocation
	} else {
		//if it isn't a provided location create the profiling directory under the default temp location if it doesn't exist
		// profilingで指定されたディレクトリがなければ(statが取得できなければ)、生成する。
		if _, err := os.Stat(profilingLocation); err != nil {
			err = os.Mkdir(profilingLocation, 0755)
			if err != nil {
				return "", fmt.Errorf("unable to create profiling location: %s", err.Error())
			}
		}
	}
	return profilingLocation, nil
}

func reloadProfilingInfo(configFileName string) (bool, string, error) {

	cfg, err := config.LoadCdnConfig(configFileName)
	if err != nil {
		return false, "", err
	}

	profilingLocation, err := getProcessedProfilingLocation(cfg.ProfilingLocation, cfg.LogLocationError)
	if err != nil {
		return false, "", err
	}

	return cfg.ProfilingEnabled, profilingLocation, nil
}

func continuousProfile(profiling *bool, profilingDir *string, version string) {

	// profilingが有効で、profiling用ディレクトリの設定が指定されていたら
	if *profiling && *profilingDir != "" {
		go func() {
			for {

				// プロファイル用のファイル名を「tocpu-<version>-<time>.pprof」として生成する
				now := time.Now().UTC()
				filename := filepath.Join(*profilingDir, fmt.Sprintf("tocpu-%s-%s.pprof", version, now.Format(time.RFC3339)))
				f, err := os.Create(filename)
				if err != nil {
					log.Errorf("creating profile: %v\n", err)
					log.Infof("Exiting profiling")
					break
				}

				// プロファイリングを計測する。 see: https://pkg.go.dev/runtime/pprof
				pprof.StartCPUProfile(f)
				time.Sleep(time.Minute)
				pprof.StopCPUProfile()

				f.Close()

				// profilingはコピーされた変数ではなく、continuousProfile()に渡ってきた参照値を見ているので、falseへの変更があればgoroutineが終了する
				if !*profiling {
					break
				}
			}
		}()
	}
}

func signalReloader(sig os.Signal, f func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, sig)  // ここでシグナルを受信するまでwaitする
	for range c {
		log.Debugln("received SIGHUP")
		f()
	}
}

func logConfig(cfg config.Config) {
	// 設定に関するログをINFOレベルのログとして出力する
	log.Infof(`Using Config values:
		Port:                 %s
		Db Server:            %s
		Db User:              %s
		Db Name:              %s
		Db Ssl:               %t
		Max Db Connections:   %d
		TO URL:               %s
		Insecure:             %t
		Cert Path:            %s
		Key Path:             %s
		Proxy Timeout:        %v
		Proxy KeepAlive:      %v
		Proxy tls handshake:  %v
		Proxy header timeout: %v
		Read Timeout:         %v
		Read Header Timeout:  %v
		Write Timeout:        %v
		Idle Timeout:         %v
		Error Log:            %s
		Warn Log:             %s
		Info Log:             %s
		Debug Log:            %s
		Event Log:            %s
		LDAP Enabled:         %v
		InfluxDB Enabled:     %v`, cfg.Port, cfg.DB.Hostname, cfg.DB.User, cfg.DB.DBName, cfg.DB.SSL, cfg.MaxDBConnections, cfg.Listen[0], cfg.Insecure, cfg.CertPath, cfg.KeyPath, time.Duration(cfg.ProxyTimeout)*time.Second, time.Duration(cfg.ProxyKeepAlive)*time.Second, time.Duration(cfg.ProxyTLSTimeout)*time.Second, time.Duration(cfg.ProxyReadHeaderTimeout)*time.Second, time.Duration(cfg.ReadTimeout)*time.Second, time.Duration(cfg.ReadHeaderTimeout)*time.Second, time.Duration(cfg.WriteTimeout)*time.Second, time.Duration(cfg.IdleTimeout)*time.Second, cfg.LogLocationError, cfg.LogLocationWarning, cfg.LogLocationInfo, cfg.LogLocationDebug, cfg.LogLocationEvent, cfg.LDAPEnabled, cfg.InfluxEnabled)
}
