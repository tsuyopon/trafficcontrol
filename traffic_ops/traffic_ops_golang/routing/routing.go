// Package routing defines the HTTP routes for Traffic Ops and provides tools to
// register those routes with appropriate middleware.
package routing

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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/api"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/auth"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/config"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/plugin"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/routing/middleware"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/trafficvault"

	"github.com/jmoiron/sqlx"
)

// RoutePrefix is a prefix that all API routes must match.
const RoutePrefix = "^api" // TODO config?

type backendConfigSynced struct {
	cfg config.BackendConfig
	*sync.RWMutex
}

// backendCfg stores the current backend config supplied to traffic ops.
var backendCfg = backendConfigSynced{RWMutex: &sync.RWMutex{}}

// GetBackendConfig returns the current BackendConfig.
func GetBackendConfig() config.BackendConfig {
	backendCfg.RLock()
	defer backendCfg.RUnlock()
	return backendCfg.cfg
}

// SetBackendConfig sets the BackendConfig to the value supplied.
func SetBackendConfig(backendConfig config.BackendConfig) {
	backendCfg.Lock()
	defer backendCfg.Unlock()
	backendCfg.cfg = backendConfig
}

// A Route defines an association with a client request and a handler for that
// request.
type Route struct {
	// Order matters! Do not reorder this! Routes() uses positional construction for readability.
	Version             api.Version
	Method              string
	Path                string
	Handler             http.HandlerFunc
	RequiredPrivLevel   int
	RequiredPermissions []string
	Authenticated       bool
	Middlewares         []middleware.Middleware
	ID                  int // unique ID for referencing this Route
}

func (r Route) String() string {
	return fmt.Sprintf("id=%d\tmethod=%s\tversion=%d.%d\tpath=%s", r.ID, r.Method, r.Version.Major, r.Version.Minor, r.Path)
}

// SetMiddleware sets up a Route's Middlewares to include the default set of
// Middlewares if necessary.
func (r *Route) SetMiddleware(authBase middleware.AuthBase, requestTimeout time.Duration) {

	if r.Middlewares == nil {
		r.Middlewares = middleware.GetDefault(authBase.Secret, requestTimeout)
	}

	// 認証済み
	if r.Authenticated { // a privLevel of zero is an unauthenticated endpoint.
		authWrapper := authBase.GetWrapper(r.RequiredPrivLevel)
		r.Middlewares = append(r.Middlewares, authWrapper)
	}

	// 認証が必要な場合
	r.Middlewares = append(r.Middlewares, middleware.RequiredPermissionsMiddleware(r.RequiredPermissions))
}

// ServerData ...
type ServerData struct {
	config.Config
	DB           *sqlx.DB
	Profiling    *bool // Yes this is a field in the config but we want to live reload this value and NOT the entire config
	Plugins      plugin.Plugins
	TrafficVault trafficvault.TrafficVault
	Mux          *http.ServeMux
}

// CompiledRoute ...
type CompiledRoute struct {
	Handler http.HandlerFunc
	Regex   *regexp.Regexp
	Params  []string
	ID      int
}

// エンドポイント一覧からAPIのメジャーバージョンとマイナーバージョンを調布君子で取得して、
// 下記のルールでソートした、api.Versionの配列を応答します。
//
// ソート時は1 -> 2の順で優先度が高いものとする
//  1. メジャーバージョンを昇順でソートする
//  2. マイナーバージョンを昇順でソートする
//
func getSortedRouteVersions(rs []Route) []api.Version {

	majorsToMinors := map[uint64][]uint64{}
	majors := map[uint64]struct{}{}

	// Route構造体に登録されたエンドポイント(routes.goのRoute()にて定義)毎に処理が行われます。
	for _, r := range rs {
		majors[r.Version.Major] = struct{}{}

		// majorsToMinorsマップがすでにメジャーバージョンに対応するスライスを持っている場合、以前に追加されたかどうかを確認するために、すでに含まれているかどうかを判定します。
		if _, ok := majorsToMinors[r.Version.Major]; ok {
			// majorsToMinorsマップがすでにメジャーバージョンに対応するスライスを持っている場合の処理

			previouslyIncluded := false

			// majorsToMinorsマップがすでにメジャーバージョンに対応するスライスを持っている場合、以前に追加されたかどうかを確認するために、すでに含まれているかどうかを判定します。
			for _, prevMinor := range majorsToMinors[r.Version.Major] {
				// メジャーバージョンに対応するマイナーバージョンでrangeループしていて、majorsToMinors[r.Version.Major][マイナーバージョン]に登録済みかを確認します。
				if prevMinor == r.Version.Minor {
					previouslyIncluded = true
				}
			}

			// 以前にそのメジャーバージョンにマイナーバージョンが追加されたことがない場合、
			// majorsToMinorsマップに、メジャーバージョンに対応するスライスに新しいマイナーバージョンを追加します。
			if !previouslyIncluded {
				majorsToMinors[r.Version.Major] = append(majorsToMinors[r.Version.Major], r.Version.Minor)
			}

		} else {
			// majorsToMinorsマップがすでにメジャーバージョンに対応するスライスを持っていない場合(以前追加されたことがない場合)、
			// majorsToMinorsマップに、メジャーバージョンに対応するスライスに新しいマイナーバージョンを追加します。
			majorsToMinors[r.Version.Major] = []uint64{r.Version.Minor}
		}
	}

	// 取得したメジャーバージョンをsortedMajoursという配列に詰めます
	sortedMajors := []uint64{}
	for major := range majors {
		sortedMajors = append(sortedMajors, major)
	}

	// 取得したメジャーバージョンの一覧を昇順にソートします。
	sort.Slice(sortedMajors, func(i, j int) bool { return sortedMajors[i] < sortedMajors[j] })

	versions := []api.Version{}

	// メジャーバージョン毎に処理を行います
	for _, major := range sortedMajors {
		// 各メジャーバージョンに属するマイナーバージョンの一覧を取得し、昇順にソートして、各バージョンを作成し、ソートされたバージョンのスライスに追加します。
		sort.Slice(majorsToMinors[major], func(i, j int) bool { return majorsToMinors[major][i] < majorsToMinors[major][j] })
		for _, minor := range majorsToMinors[major] {
			version := api.Version{Major: major, Minor: minor}

			// つまり、下記には メジャーバージョンはメジャーバージョンの昇順、その後メジャーバージョンに属するマイナーバージョンの昇順で並び替えられます。
			// 下記のような順番でappendされることになります。
			// 例 api.Version{Major: 3, Minor: 1}, api.Version{Major: 3, Minor: 2}, api.Version{Major: 3, Minor: 3}, api.Version{Major: 4, Minor: 2}, api.Version{Major: 4, Minor: 3}, api.Version{Major: 3, Minor: 4}
			versions = append(versions, version)
		}
	}

	return versions
}

func indexOfApiVersion(versions []api.Version, desiredVersion api.Version) int {
	for i, v := range versions {
		if v.Major > desiredVersion.Major {
			return i
		}
		if v.Major == desiredVersion.Major && v.Minor >= desiredVersion.Minor {
			return i
		}
	}
	return len(versions) - 1
}

// PathHandler ...
type PathHandler struct {
	Path    string
	Handler http.HandlerFunc
	ID      int
}

// CreateRouteMap returns a map of methods to a slice of paths and handlers; wrapping the handlers in the appropriate middleware. Uses Semantic Versioning: routes are added to every subsequent minor version, but not subsequent major versions. For example, a 1.2 route is added to 1.3 but not 2.1. Also truncates '2.0' to '2', creating succinct major versions.
// Returns the map of routes, and a map of API versions served.
//
// 第３引数のperlHandlerは特に使われてなさそう
func CreateRouteMap(rs []Route, disabledRouteIDs []int, perlHandler http.HandlerFunc, authBase middleware.AuthBase, reqTimeOutSeconds int) (map[string][]PathHandler, map[api.Version]struct{}) {

	// TODO strong types for method, path
	versions := getSortedRouteVersions(rs)

	// TODO: 不明
	requestTimeout := middleware.DefaultRequestTimeout
	if reqTimeOutSeconds > 0 {
		requestTimeout = time.Second * time.Duration(reqTimeOutSeconds)
	}

	// disabled_routes設定されたIDを元に配列を形成する
	disabledRoutes := GetRouteIDMap(disabledRouteIDs)
	m := map[string][]PathHandler{}

	// APIエンドポイント毎のrange
	for _, r := range rs {
		versionI := indexOfApiVersion(versions, r.Version)
		nextMajorVer := r.Version.Major + 1
		_, isDisabledRoute := disabledRoutes[r.ID]
		r.SetMiddleware(authBase, requestTimeout)

		// バージョン毎のrange
		for _, version := range versions[versionI:] {

			if version.Major >= nextMajorVer {
				break
			}

			vstr := strconv.FormatUint(version.Major, 10) + "." + strconv.FormatUint(version.Minor, 10)

			// "^api/<v>/<path>"
			path := RoutePrefix + "/" + vstr + "/" + r.Path

			if isDisabledRoute {
				// disabled_routesされている場合には、DisabledRouteHandler()というリクエストを禁止するメッセージのエンドポイントを設定する
				m[r.Method] = append(m[r.Method], PathHandler{Path: path, Handler: middleware.WrapAccessLog(authBase.Secret, middleware.DisabledRouteHandler()), ID: r.ID})
			} else {
				m[r.Method] = append(m[r.Method], PathHandler{Path: path, Handler: middleware.Use(r.Handler, r.Middlewares), ID: r.ID})
			}
			log.Infof("adding route %v %v\n", r.Method, path)
		}
	}

	versionSet := map[api.Version]struct{}{}
	for _, version := range versions {
		versionSet[version] = struct{}{}
	}

	return m, versionSet
}


// CompileRoutes - takes a map of methods to paths and handlers, and returns a map of methods to CompiledRoutes
// この関数は、与えられたルート情報(例: 「OC/CI/configuration/request/{id}/{approved}」)を正規表現を使ってコンパイルされたルートに変換するために必要な事前準備としてのオブジェクトを生成しています。
func CompileRoutes(routes map[string][]PathHandler) map[string][]CompiledRoute {

	compiledRoutes := map[string][]CompiledRoute{}

	// APIエンドポイント毎にループ処理を行う
	// routesはindexにメソッド名(GET等)、keyにpathHandler構造体が含まれる
	for method, mRoutes := range routes {
		for _, pathHandler := range mRoutes {

			// 「OC/CI/configuration/request/{id}/{approved}」のようなパス情報が含まれます。
			route := pathHandler.Path
			handler := pathHandler.Handler
			var params []string

			// "{"が見つかった１のindexから順番に処理をしていく
			// 「OC/CI/configuration/request/{id}/{approved}」のようなAPIエンドポイントを表すstringsに対して処理をしていくことになります。
			for open := strings.Index(route, "{"); open > 0; open = strings.Index(route, "{") {

				// 閉じかっこ"}"が見つかったらcloseとする
				close := strings.Index(route, "}")

				// "}"が存在しなかったらcloseには-1が入ります。APIエンドポイントのrouteには必ず"{"が含まれる場合には"}"も対として含まれますが、このケースでは"}"がないので不正なルート設定としています。
				if close < 0 {
					panic("malformed route")
				}

				// "{"から"}"までを取得してparamに格納する。この時"{"と"}"は含まれない範囲指定となっている。
				param := route[open+1 : close]

				// "{"から"}"が複数あれば、
				params = append(params, param)

				// "{"から"}"で指定された箇所が後で置換できるように正規表現にしておきます。
				route = route[:open] + `([^/]+)` + route[close+1:]
			}

			// Routeの正規表現を有効にする (手前のロジックで必ず"([^/]+)"を付与しているので正規表現となる。
			regex := regexp.MustCompile(route)
			id := pathHandler.ID

			// compiledRoutesスライスに詰めます
			compiledRoutes[method] = append(compiledRoutes[method], CompiledRoute{Handler: handler, Regex: regex, Params: params, ID: id})
		}
	}

	return compiledRoutes
}

// Handler - generic handler func used by the Handlers hooking into the routes
func Handler(
	routes map[string][]CompiledRoute,
	versions map[api.Version]struct{},
	catchall http.Handler,
	db *sqlx.DB,
	cfg *config.Config,
	getReqID func() uint64,
	plugins plugin.Plugins,
	tv trafficvault.TrafficVault,
	w http.ResponseWriter,
	r *http.Request,
) {
	reqID := getReqID()

	reqIDStr := strconv.FormatUint(reqID, 10)
	log.Infoln(r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery + " handling (reqid " + reqIDStr + ")")
	start := time.Now()
	defer func() {
		log.Infoln(r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery + " handled (reqid " + reqIDStr + ") in " + time.Since(start).String())
	}()

	ctx := r.Context()
	ctx = context.WithValue(ctx, api.DBContextKey, db)
	ctx = context.WithValue(ctx, api.ConfigContextKey, cfg)
	ctx = context.WithValue(ctx, api.ReqIDContextKey, reqID)
	ctx = context.WithValue(ctx, api.TrafficVaultContextKey, tv)

	// plugins have no pre-parsed path params, but add an empty map so they can use the api helper funcs that require it.
	pluginCtx := context.WithValue(ctx, api.PathParamsKey, map[string]string{})
	pluginReq := r.WithContext(pluginCtx)

	onReqData := plugin.OnRequestData{Data: plugin.Data{RequestID: reqID, AppCfg: *cfg}, W: w, R: pluginReq}
	if handled := plugins.OnRequest(onReqData); handled {
		return
	}

	requested := r.URL.Path[1:]
	mRoutes, ok := routes[r.Method]
	if !ok {
		catchall.ServeHTTP(w, r)
		return
	}

	for _, compiledRoute := range mRoutes {
		match := compiledRoute.Regex.FindStringSubmatch(requested)
		if len(match) == 0 {
			continue
		}
		params := map[string]string{}
		for i, v := range compiledRoute.Params {
			params[v] = match[i+1]
		}

		routeCtx := context.WithValue(ctx, api.PathParamsKey, params)
		routeCtx = context.WithValue(routeCtx, middleware.RouteID, compiledRoute.ID)
		r = r.WithContext(routeCtx)
		compiledRoute.Handler(w, r)
		return
	}

	// リクエストされたAPIが不明なバージョンを含む場合にはNotImplementedHandler()が呼ばれる
	if IsRequestAPIAndUnknownVersion(r, versions) {
		h := middleware.WrapAccessLog(cfg.Secrets[0], middleware.NotImplementedHandler())
		h.ServeHTTP(w, r)
		return
	}

	var backendRouteHandled bool
	backendConfig := GetBackendConfig()
	// 下記のロジックは--backendにより設定が追加された場合の処理
	for i, backendRoute := range backendConfig.Routes {
		var params []string
		routeParams := map[string]string{}
		if backendRoute.Method == r.Method {
			for open := strings.Index(backendRoute.Path, "{"); open > 0; open = strings.Index(backendRoute.Path, "{") {
				close := strings.Index(backendRoute.Path, "}")
				if close < 0 {
					panic("malformed route")
				}
				param := backendRoute.Path[open+1 : close]
				params = append(params, param)
				backendRoute.Path = backendRoute.Path[:open] + `([^/]+)` + backendRoute.Path[close+1:]
			}
			regex := regexp.MustCompile(backendRoute.Path)
			match := regex.FindStringSubmatch(r.URL.Path)
			if len(match) == 0 {
				continue
			}
			for i, v := range params {
				routeParams[v] = match[i+1]
			}

			// 
			if backendRoute.Opts.Algorithm == "" || backendRoute.Opts.Algorithm == "roundrobin" {
				index := backendRoute.Index % len(backendRoute.Hosts)
				host := backendRoute.Hosts[index]
				backendRoute.Index++
				backendConfig.Routes[i] = backendRoute
				backendRouteHandled = true
				rp := httputil.NewSingleHostReverseProxy(&url.URL{
					Host:   host.Hostname + ":" + strconv.Itoa(host.Port),
					Scheme: host.Protocol,
				})
				rp.Transport = &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: backendRoute.Insecure},
				}
				rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
					api.HandleErr(w, r, nil, http.StatusInternalServerError, nil, err)
					return
				}
				routeCtx := context.WithValue(ctx, api.DBContextKey, db)
				routeCtx = context.WithValue(routeCtx, api.PathParamsKey, routeParams)
				routeCtx = context.WithValue(routeCtx, middleware.RouteID, backendRoute.ID)
				r = r.WithContext(routeCtx)
				userErr, sysErr, code := HandleBackendRoute(cfg, backendRoute, w, r)
				if userErr != nil || sysErr != nil {
					h2 := middleware.WrapAccessLog(cfg.Secrets[0], middleware.BackendErrorHandler(code, userErr, sysErr))
					h2.ServeHTTP(w, r)
					return
				}
				backendHandler := middleware.WrapAccessLog(cfg.Secrets[0], rp)
				backendHandler.ServeHTTP(w, r)
				return
			} else {
				h2 := middleware.WrapAccessLog(cfg.Secrets[0], middleware.BackendErrorHandler(http.StatusBadRequest, errors.New("only an algorithm of roundrobin is supported by the backend options currently"), nil))
				h2.ServeHTTP(w, r)
				return
			}
		}
	}

	if !backendRouteHandled {
		catchall.ServeHTTP(w, r)
	}
}

// HandleBackendRoute does all the pre processing for the backend routes.
func HandleBackendRoute(cfg *config.Config, route config.BackendRoute, w http.ResponseWriter, r *http.Request) (error, error, int) {
	var userErr, sysErr error
	var errCode int
	var user auth.CurrentUser
	var inf *api.APIInfo

	user, userErr, sysErr, errCode = api.GetUserFromReq(w, r, cfg.Secrets[0])
	if userErr != nil || sysErr != nil {
		return userErr, sysErr, errCode
	}
	if cfg.RoleBasedPermissions {
		missingPerms := user.MissingPermissions(route.Permissions...)
		if len(missingPerms) != 0 {
			msg := strings.Join(missingPerms, ", ")
			return fmt.Errorf("missing required Permissions: %s", msg), nil, http.StatusForbidden
		}
	}
	api.AddUserToReq(r, user)
	var params []string
	inf, userErr, sysErr, errCode = api.NewInfo(r, params, nil)
	if userErr != nil || sysErr != nil {
		return userErr, sysErr, errCode
	}
	defer inf.Close()
	return nil, nil, http.StatusOK
}

// IsRequestAPIAndUnknownVersion returns true if the request starts with `/api` and is a version not in the list of versions.
func IsRequestAPIAndUnknownVersion(req *http.Request, versions map[api.Version]struct{}) bool {

	// "/"でパースする。「/api/4.0/hogehoge」のような形式のURLがパースされる
	pathParts := strings.Split(req.URL.Path, "/")
	if len(pathParts) < 2 {
		return false // path doesn't start with `/api`, so it's not an api request
	}

	// 1つ目は「api」でなければエラー
	if strings.ToLower(pathParts[1]) != "api" {
		return false // path doesn't start with `/api`, so it's not an api request
	}

	// 3つの"/"でセパレートすると「/api/4.0/hogehoge」は 「api  4.0  hogehoge」のように分割される必要がある。このため、3つ以下ならエラー
	if len(pathParts) < 3 {
		return true // path starts with `/api` but not `/api/{version}`, so it's an api request, and an unknown/nonexistent version.
	}

	// バージョンフィールド(2番目)を渡してバージョン構造体に変換する
	version, err := stringVersionToApiVersion(pathParts[2])
	if err != nil { // パースできなかったので不明バージョン
		return true // path starts with `/api`, and version isn't a number, so it's an unknown/nonexistent version
	}

	// 指定されたバージョンが存在する場合
	if _, versionExists := versions[version]; versionExists {
		return false // path starts with `/api` and version exists, so it's API but a known version
	}

	// パスは「/api」で始まりバージョンもパースできたが、存在しないバージョン
	return true // path starts with `/api`, and version is unknown
}

func stringVersionToApiVersion(version string) (api.Version, error) {

	// ドットでバージョンをsplitする
	versionParts := strings.Split(version, ".")

	// ドットでsplitして2つ以上のフィールドがなければエラー
	if len(versionParts) < 2 {
		return api.Version{}, errors.New("error parsing version " + version)
	}

	// メジャーバージョンの取得 (1つ目のフィールド)
	major, err := strconv.ParseUint(versionParts[0], 10, 64)
	if err != nil {
		return api.Version{}, errors.New("error parsing version " + version)
	}

	// マイナーバージョンの取得 (2つ目のフィールド)
	minor, err := strconv.ParseUint(versionParts[1], 10, 64)
	if err != nil {
		return api.Version{}, errors.New("error parsing version " + version)
	}

	// APIバージョンを構造体形式で応答する
	return api.Version{Major: major, Minor: minor}, nil
}

// RegisterRoutes - parses the routes and registers the handlers with the Go Router
// TrafficOpsのAPIエンドポイント設定となる主要処理
func RegisterRoutes(d ServerData) error {

	// **重要** 下記のRoutes(d)でAPIエンドポイントの登録が行われる重要な箇所です
	// routing/routes.goが呼ばれてAPIのRoute情報がrouteSliveに保存される
	routeSlice, catchall, err := Routes(d)
	if err != nil {
		// APIエンドポイントに重複したIDがセットされているか、disabled_routesにセットされたAPIエンドポイントのIDが存在せず、ignore_unknown_routes=falseの場合にはエラーになる
		return err
	}

	authBase := middleware.AuthBase{Secret: d.Config.Secrets[0], Override: nil} //we know d.Config.Secrets is a slice of at least one or start up would fail.

	// エンドポイント毎にオブジェクトを作成する
	// この際にdisableなエンドポイントかやどうかや、認証失敗時のハンドラ、リクエストタイムアウト時の時刻などをそれぞれ設定したオブジェクトを変換する
	routes, versions := CreateRouteMap(routeSlice, d.DisabledRoutes, handlerToFunc(catchall), authBase, d.RequestTimeout)

	compiledRoutes := CompileRoutes(routes)
	getReqID := nextReqIDGetter()

	// HTTPサーバにAPIエンドポイントの登録を行う
	d.Mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		Handler(compiledRoutes, versions, catchall, d.DB, &d.Config, getReqID, d.Plugins, d.TrafficVault, w, r)
	})

	return nil
}

// nextReqIDGetter returns a function for getting incrementing identifiers. The returned func is safe for calling with multiple goroutines. Note the returned identifiers will not be unique after the max uint64 value.
func nextReqIDGetter() func() uint64 {
	id := uint64(0)
	return func() uint64 {
		return atomic.AddUint64(&id, 1)
	}
}
