package plugin

/*
   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/config"
)

// List returns the list of plugin names compiled into the calling executable.
func List() []string {
	l := []string{}
	for _, p := range initPlugins {
		l = append(l, p.info.Name)
	}
	return l
}

// appCfg.Pluginsに設定された有効なプラグイン情報を取得する
func Get(appCfg config.Config) Plugins {

	log.Infof("plugin.Get given: %+v\n", appCfg.Plugins)

	// appCfg.Pluginsに指定されたプラグインのうち、実際に有効なプラグインを取得する(ソートもされる)
	// cdn.confに指定された「plugin」設定を追加した場合 (サンプルがなかった)
	pluginSlice := getEnabled(appCfg.Plugins)
	
	// cdn.confに指定された「plugin_config」の設定が入る
	// 設定例: {"plugin_config": {"hello_config":{"hello": "anything can go here"}}}
	pluginCfg := loadConfig(pluginSlice, appCfg.PluginConfig)

	ctx := map[string]*interface{}{}
	return plugins{slice: pluginSlice, cfg: pluginCfg, ctx: ctx}

}

func getEnabled(enabled []string) pluginsSlice {

	// struct{}は空の型を表し、struct{}{}は空の値を表す。代入する場合には struct{}{} にする必要がある
	enabledM := map[string]struct{}{}
	for _, name := range enabled {
		enabledM[name] = struct{}{}
	}

	enabledPlugins := pluginsSlice{}

	// initPluginsは初期化時に登録されたプラグインリストである
	for _, plugin := range initPlugins {

		// 指定されたプラグインが「initPlugins」の初期化時に登録されたプラグイン情報に含まれているかをチェックする。
		if _, ok := enabledM[plugin.info.Name]; !ok {
			// 初期化時に登録されたプラグインが、引数としては指定されていないプラグインの場合
			log.Infoln("getEnabled skipping: '" + plugin.info.Name + "'")
			continue
		}

		// 初期化時に登録されたプラグインが、getEnabledの引数に指定されたプラグインの場合にはenabledPluginsに追加する
		log.Infoln("plugin enabling: '" + plugin.info.Name + "'")
		enabledPlugins = append(enabledPlugins, plugin)
	}

	// 有効となるプラグイン一覧をソートして応答する
	sort.Sort(enabledPlugins)
	return enabledPlugins
}

func loadConfig(ps pluginsSlice, configJSON map[string]json.RawMessage) map[string]interface{} {
	pluginConfigLoaders := loadFuncs(ps)
	cfg := make(map[string]interface{}, len(configJSON))
	for name, b := range configJSON {
		if loadF := pluginConfigLoaders[name]; loadF != nil {
			cfg[name] = loadF(b)
		}
	}
	return cfg
}

func loadFuncs(ps pluginsSlice) map[string]LoadFunc {
	lf := map[string]LoadFunc{}
	for _, plugin := range ps {
		if plugin.funcs.load == nil {
			continue
		}
		lf[plugin.info.Name] = LoadFunc(plugin.funcs.load)
	}
	return lf
}

type Plugins interface {
	OnStartup(d StartupData)
	OnRequest(d OnRequestData) bool
	GetInfo() []Info
}

func AddPlugin(priority uint64, funcs Funcs, description, version string) {

	_, filename, _, ok := runtime.Caller(1)
	if !ok {
		fmt.Println(time.Now().Format(time.RFC3339Nano) + " Error plugin.AddPlugin: runtime.Caller failed, can't get plugin names") // print, because this is called in init, loggers don't exist yet
		os.Exit(1)
	}

	pluginName := strings.TrimSuffix(path.Base(filename), ".go")
	log.Debugln("AddPlugin adding " + pluginName)
	i := Info{
		Name:        pluginName,
		Description: description,
		Version:     version,
	}
	initPlugins = append(initPlugins, pluginObj{funcs: funcs, priority: priority, info: i})
}

type Funcs struct {
	load      LoadFunc
	onStartup StartupFunc
	onRequest OnRequestFunc
}

// Data is the common plugin data, given to most plugin hooks. This is designed to be embedded in the data structs for specific hooks.
type Data struct {
	Cfg       interface{}
	Ctx       *interface{}
	SharedCfg map[string]interface{}
	RequestID uint64
	AppCfg    config.Config
}

type StartupData struct {
	Data
}

type OnRequestData struct {
	Data
	W http.ResponseWriter
	R *http.Request
}

type IsRequestHandled bool

const (
	RequestHandled   = IsRequestHandled(true)
	RequestUnhandled = IsRequestHandled(false)
)

type LoadFunc func(json.RawMessage) interface{}
type StartupFunc func(d StartupData)
type OnRequestFunc func(d OnRequestData) IsRequestHandled

type pluginObj struct {
	funcs    Funcs
	priority uint64
	info     Info
}

type Info struct {
	Name        string
	Description string
	Version     string
}

type plugins struct {
	slice pluginsSlice
	cfg   map[string]interface{}
	ctx   map[string]*interface{}
}

type pluginsSlice []pluginObj

func (p pluginsSlice) Len() int           { return len(p) }
func (p pluginsSlice) Less(i, j int) bool { return p[i].priority < p[j].priority }
func (p pluginsSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// initPlugins is where plugins are registered via their init functions.
var initPlugins = pluginsSlice{}

func (ps plugins) OnStartup(d StartupData) {

	// プラグイン毎にイテレーションする
	// ps.sliceはmainでの「plugins := plugin.Get(cfg)」の結果で渡されてきたプラグインのスライスを表します。
	for _, p := range ps.slice {
		ictx := interface{}(nil)
		ps.ctx[p.info.Name] = &ictx

		// onStartupが登録されていない場合にはその後のOnStartupのハンドラ実行を行いません。
		if p.funcs.onStartup == nil {
			continue
		}

		d.Ctx = ps.ctx[p.info.Name]
		d.Cfg = ps.cfg[p.info.Name]

		// ここが主要な処理、OnStartupとして渡されたハンドラを実行する
		// ここでのonStartupが実行する具体的な関数は「AddPlugin」関数を実行時に指定する際に指定されている(「AddPlugin」でgrepして調べると良い)
		p.funcs.onStartup(d)   // AddPluginのプラグイン関数設定時にonStartupの値も明示的に指定しているのでそこでonStartupの関数ハンドラが決定する

	}
}

// OnRequest returns a boolean whether to immediately stop processing the request. If a plugin returns true, this is immediately returned with no further plugins processed.
func (ps plugins) OnRequest(d OnRequestData) bool {
	log.Debugf("DEBUG plugins.OnRequest calling %+v plugins\n", len(ps.slice))
	for _, p := range ps.slice {
		if p.funcs.onRequest == nil {
			log.Debugln("plugins.OnRequest plugging " + p.info.Name + " - no onRequest func")
			continue
		}
		d.Ctx = ps.ctx[p.info.Name]
		d.Cfg = ps.cfg[p.info.Name]
		log.Debugln("plugins.OnRequest plugging " + p.info.Name)
		if stop := p.funcs.onRequest(d); stop {
			return true
		}
	}
	return false
}

func (ps plugins) GetInfo() []Info {
	pluginsInfo := []Info{}
	for _, p := range ps.slice {
		pluginsInfo = append(pluginsInfo, p.info)
	}
	return pluginsInfo
}
