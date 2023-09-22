package torequest

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
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/apache/trafficcontrol/cache-config/t3c-apply/config"
	"github.com/apache/trafficcontrol/cache-config/t3c-apply/util"
	"github.com/apache/trafficcontrol/cache-config/t3cutil"
	"github.com/apache/trafficcontrol/lib/go-log"
)

type UpdateStatus int

const (
	UpdateTropsNotNeeded  UpdateStatus = 0
	UpdateTropsNeeded     UpdateStatus = 1
	UpdateTropsSuccessful UpdateStatus = 2
	UpdateTropsFailed     UpdateStatus = 3
)

type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type TrafficOpsReq struct {
	Cfg     config.Cfg
	pkgs    map[string]bool // map of packages which are installed, either already installed or newly installed by this run.
	plugins map[string]bool // map of verified plugins

	installedPkgs map[string]struct{} // map of packages which were installed by us.
	changedFiles  []string            // list of config files which were changed

	configFiles        map[string]*ConfigFile
	configFileWarnings map[string][]string

	RestartData
}

type ShouldReloadRestart struct {
	ReloadRestart []FileRestartData
}

type FileRestartData struct {
	Name string
	RestartData
}

type RestartData struct {
	TrafficCtlReload     bool // a traffic_ctl_reload is required
	SysCtlReload         bool // a reload of the sysctl.conf is required
	NtpdRestart          bool // ntpd needs restarting
	TeakdRestart         bool // a restart of teakd is required
	TrafficServerRestart bool // a trafficserver restart is required
	RemapConfigReload    bool // remap.config should be reloaded
}

type ConfigFile struct {
	Name              string // file name
	Dir               string // install directory
	Path              string // full path
	Service           string // service assigned to
	CfgBackup         string // location to backup the config at 'Path'
	TropsBackup       string // location to backup the TrafficOps Version
	AuditComplete     bool   // audit is complete
	AuditFailed       bool   // audit failed
	ChangeApplied     bool   // a change has been applied
	ChangeNeeded      bool   // change required
	PreReqFailed      bool   // failed plugin prerequiste check
	RemapPluginConfig bool   // file is a remap plugin config file
	Body              []byte
	Perm              os.FileMode // default file permissions
	Uid               int         // owner uid, default is 0
	Gid               int         // owner gid, default is 0
	Warnings          []string
}

func (u UpdateStatus) String() string {
	var result string
	switch u {
	case 0:
		result = "UpdateTropsNotNeeded"
	case 1:
		result = "UpdateTropsNeeded"
	case 2:
		result = "UpdateTropsSuccessful"
	case 3:
		result = "UpdateTropsFailed"
	}
	return result
}

// commentsFilter is used to remove comment
// lines from config files while making
// comparisons.
func commentsFilter(body []string) []string {
	var newlines []string

	newlines = make([]string, 0)

	for ii := range body {
		line := body[ii]
		if strings.HasPrefix(line, "#") {
			continue
		}
		newlines = append(newlines, line)
	}

	return newlines
}

// newLineFilter removes carriage returns
// from config files while making comparisons.
func newLineFilter(str string) string {
	str = strings.ReplaceAll(str, "\r\n", "\n")
	return strings.TrimSpace(str)
}

// unencodeFilter translates HTML escape
// sequences while making config file comparisons.
func unencodeFilter(body []string) []string {
	var newlines []string

	newlines = make([]string, 0)
	sp := regexp.MustCompile(`\s+`)
	el := regexp.MustCompile(`^\s+|\s+$`)
	am := regexp.MustCompile(`amp;`)
	lt := regexp.MustCompile(`&lt;`)
	gt := regexp.MustCompile(`&gt;`)

	for ii := range body {
		s := body[ii]
		s = sp.ReplaceAllString(s, " ")
		s = el.ReplaceAllString(s, "")
		s = am.ReplaceAllString(s, "")
		s = lt.ReplaceAllString(s, "<")
		s = gt.ReplaceAllString(s, ">")
		s = strings.TrimSpace(s)
		newlines = append(newlines, s)
	}

	return newlines
}

// DumpConfigFiles is used for debugging
func (r *TrafficOpsReq) DumpConfigFiles() {
	for _, cfg := range r.configFiles {
		log.Infof("Name: %s, Dir: %s, Service: %s\n",
			cfg.Name, cfg.Dir, cfg.Service)
	}
}

// NewTrafficOpsReq returns a new TrafficOpsReq object.
func NewTrafficOpsReq(cfg config.Cfg) *TrafficOpsReq {
	return &TrafficOpsReq{
		Cfg:           cfg,
		pkgs:          map[string]bool{},
		plugins:       map[string]bool{},
		configFiles:   map[string]*ConfigFile{},
		installedPkgs: map[string]struct{}{},
	}
}

// checkConfigFile checks and audits config files.
// The filesAdding parameter is the list of files about to be added, which is needed for verification in case a file is required and about to be created but doesn't exist yet.
// ファイル毎にこの関数が呼び出されます。呼び出し元ではこの関数はrangeでイテレーションして呼ばれています。
func (r *TrafficOpsReq) checkConfigFile(cfg *ConfigFile, filesAdding []string) error {

	// 空のファイルが指定された場合にはエラー
	if cfg.Name == "" {
		cfg.AuditFailed = true
		return errors.New("Config file name is empty is empty, skipping further checks.")
	}

	// 空のディレクトリが指定された場合にはエラー
	if cfg.Dir == "" {
		return errors.New("No location information for " + cfg.Name)
	}
	// return if audit has already been done.
	if cfg.AuditComplete == true {
		return nil
	}

	// 指定されたディレクトリがmkdirしたり、指定されたuid, gidでchownする。
	if !util.MkDirWithOwner(cfg.Dir, r.Cfg, &cfg.Uid, &cfg.Gid) {
		return errors.New("Unable to create the directory '" + cfg.Dir + " for " + "'" + cfg.Name + "'")
	}

	log.Debugf("======== Start processing config file: %s ========\n", cfg.Name)

	// remap.configが対象であれば
	if cfg.Name == "remap.config" {
		err := r.processRemapOverrides(cfg)
		if err != nil {
			return err
		}
	}

	// perform plugin verification
	if cfg.Name == "remap.config" || cfg.Name == "plugin.config" {
		if err := checkRefs(r.Cfg, cfg.Body, filesAdding); err != nil {
			r.configFileWarnings[cfg.Name] = append(r.configFileWarnings[cfg.Name], "failed to verify '"+cfg.Name+"': "+err.Error())
			return errors.New("failed to verify '" + cfg.Name + "': " + err.Error())
		}
		log.Infoln("Successfully verified plugins used by '" + cfg.Name + "'")
	}

	// .cer拡張子を持ったファイルがあればX509証明書として妥当かどうかをcheckCert()により検証する
	// checkCert()はParseCertificate()でX.509フォーマットに一致しているかや有効期限が問題ないかを検証する。
	if strings.HasSuffix(cfg.Name, ".cer") {
		if err := checkCert(cfg.Body); err != nil {
			r.configFileWarnings[cfg.Name] = append(r.configFileWarnings[cfg.Name], fmt.Sprintln(err))
		}
		for _, wrn := range cfg.Warnings {
			r.configFileWarnings[cfg.Name] = append(r.configFileWarnings[cfg.Name], wrn)
		}
	}

	// t3c-diffにファイルを指定することで、その設定ファイルの差分情報をTrafficOps APIから取得する
	changeNeeded, err := diff(r.Cfg, cfg.Body, cfg.Path, r.Cfg.ReportOnly, cfg.Perm, cfg.Uid, cfg.Gid)

	if err != nil {
		return errors.New("getting diff: " + err.Error())
	}
	cfg.ChangeNeeded = changeNeeded
	cfg.AuditComplete = true

	// ファイル名が50-ats.rulesの場合にだけはr.processUdevRulesを実行する。
	if cfg.Name == "50-ats.rules" {
		err := r.processUdevRules(cfg)
		if err != nil {
			return errors.New("unable to process udev rules in '" + cfg.Name + "': " + err.Error())
		}
	}

	log.Infof("======== End processing config file: %s for service: %s ========\n", cfg.Name, cfg.Service)
	return nil
}

// checkStatusFiles ensures that the cache status files reflect
// the status retrieved from Traffic Ops.
// /var/lib/trafficcontrol-cache-config/status/に存在するステータスファイルのステータスに変更があればファイルを変更する
// この関数の引数で指定されるsvrStatusには「REPORTED」のような次回更新のステータス(--get-data=update-statusで取得した次回更新時のステータスが入る)
func (r *TrafficOpsReq) checkStatusFiles(svrStatus string) error {

	// 指定されたサーバステータス(svrStatus)の値が空であれば、エラー
	if svrStatus == "" {
		return errors.New("Returning; did not find status from Traffic Ops!")
	} else {
		log.Debugf("Found %s status from Traffic Ops.\n", svrStatus)
	}

	// statusファイルのパス
	statusFile := filepath.Join(config.StatusDir, svrStatus)  // 「/var/lib/trafficcontrol-cache-config/status/REPORTED」 のようなファイルパスとなる。
	fileExists, _ := util.FileExists(statusFile)
	if !fileExists {
		log.Errorf("status file %s does not exist.\n", statusFile)
	}

	// t3c-request --get-data=statuses を実行することで、現行のサーバステータスを取得することができる
	statuses, err := getStatuses(r.Cfg)
	if err != nil {
		return fmt.Errorf("could not retrieves a statuses list from Traffic Ops: %s\n", err)
	}

	// TODO: rangeで回しているのはいったいなぜか?
	for f := range statuses {
		otherStatus := filepath.Join(config.StatusDir, statuses[f])
		// 次回更新予定のステータスと現状のステータスをによる生成したファイルパスを比較して、同一ならば何もしない
		if otherStatus == statusFile {
			continue
		}

		fileExists, _ := util.FileExists(otherStatus)
		// --report-only=false かつ 現状のステータス(otherStatus)がtrueならば、ステータスのアップデートがあったとみなして以前の状態ファイル(例: REPORTED)を削除する。
		if !r.Cfg.ReportOnly && fileExists {
			log.Errorf("Removing other status file %s that exists\n", otherStatus)
			err = os.Remove(otherStatus)
			if err != nil {
				log.Errorf("Error removing %s: %s\n", otherStatus, err)
			}
		}
	}

	// --report-only=falseの場合、statusFile用のディレクトリを生成して、statusFileに対してtouchする
	if !r.Cfg.ReportOnly {
		// statusを配置するディレクトリ(/var/lib/trafficcontrol-cache-config/status/)を生成しておく
		if !util.MkDir(config.StatusDir, r.Cfg) {
			return fmt.Errorf("unable to create '%s'\n", config.StatusDir)
		}

		// statusFileが存在していなければtouchしてstatusFileを生成する。
		fileExists, _ := util.FileExists(statusFile)
		if !fileExists {
			err = util.Touch(statusFile)
			if err != nil {
				return fmt.Errorf("unable to touch %s - %s\n", statusFile, err)
			}
		}
	}
	return nil
}

// processRemapOverrides processes remap overrides found from Traffic Ops.
// 呼び出し元を確認した際にcfgには「remap.config」の値しか含まれない
func (r *TrafficOpsReq) processRemapOverrides(cfg *ConfigFile) error {
	from := ""
	newlines := []string{}
	lineCount := 0
	overrideCount := 0
	overridenCount := 0
	overrides := map[string]int{}
	data := cfg.Body

	// remap.configの中身(cfg.Body)が0byte以上の場合に処理を行う
	if len(data) > 0 {

		lines := strings.Split(string(data), "\n")
		// 改行毎に処理を行う
		for ii := range lines {
			str := lines[ii]
			fields := strings.Fields(str)
			if str == "" || len(fields) < 2 {
				continue
			}
			lineCount++
			from = fields[1]

			_, ok := overrides[from]
			if ok == true { // check if this line should be overriden
				// see. https://github.com/apache/trafficcontrol/blob/master/docs/source/admin/traffic_server.rst
				// https://traffic-control-cdn.readthedocs.io/en/latest/admin/traffic_server.html#remap-override
				newstr := "##OVERRIDDEN## " + str
				newlines = append(newlines, newstr)
				overridenCount++
			} else if fields[0] == "##OVERRIDE##" { // check for an override
				from = fields[2]
				newlines = append(newlines, "##OVERRIDE##")
				// remove the ##OVERRIDE## comment along with the trailing space
				newstr := strings.TrimPrefix(str, "##OVERRIDE## ")
				// save the remap 'from field' to overrides.
				overrides[from] = 1
				newlines = append(newlines, newstr)
				overrideCount++
			} else { // no override is necessary
				newlines = append(newlines, str)
			}
		}
	} else {
		return errors.New("The " + cfg.Name + " file is empty, nothing to process.")
	}

	// 「##OVERRIDE##」の数が存在すれば
	if overrideCount > 0 {
		log.Infof("Overrode %d old remap rule(s) with %d new remap rule(s).\n",
			overridenCount, overrideCount)
		newdata := strings.Join(newlines, "\n")
		// strings.Join doesn't add a newline character to
		// the last element in the array and we need one
		// when the data is written out to a file.
		if !strings.HasSuffix(newdata, "\n") {
			newdata = newdata + "\n"
		}
		body := []byte(newdata)
		cfg.Body = body
	}
	return nil
}

// processUdevRules verifies disk drive device ownership and mode
// TBD: 確認したい
func (r *TrafficOpsReq) processUdevRules(cfg *ConfigFile) error {
	var udevDevices map[string]string

	data := string(cfg.Body)
	lines := strings.Split(data, "\n")

	udevDevices = make(map[string]string)
	for ii := range lines {
		var owner string
		var device string
		line := lines[ii]
		if strings.HasPrefix(line, "KERNEL==") {
			vals := strings.Split(line, "\"")
			if len(vals) >= 3 {
				device = vals[1]
				owner = vals[3]
				if owner == "root" {
					continue
				}
				userInfo, err := user.Lookup(owner)
				if err != nil {
					log.Errorf("no such user on this system: '%s'\n", owner)
					continue
				} else {
					devPath := "/dev/" + device
					fileExists, fileInfo := util.FileExists(devPath)
					if fileExists {
						udevDevices[device] = devPath
						log.Infof("Found device in 50-ats.rules: %s\n", devPath)
						if statStruct, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
							uid := strconv.Itoa(int(statStruct.Uid))
							if uid != userInfo.Uid {
								log.Errorf("Device %s is owned by uid %s, not %s (%s)\n", devPath, uid, owner, userInfo.Uid)
							} else {
								log.Infof("Ownership for disk device %s, is okay\n", devPath)
							}
						} else {
							log.Errorf("Unable to read device owner info for %s\n", devPath)
						}
					}
				}
			}
		}
	}

	// 「/proc/fs/ext4」をチェックします。ext4でなければエラーになります。
	fs, err := ioutil.ReadDir("/proc/fs/ext4")
	if err != nil {
		log.Errorln("unable to read /proc/fs/ext4, cannot audit disks for filesystem usage.")
	} else {
		for _, disk := range fs {
			for k, _ := range udevDevices {
				if strings.HasPrefix(k, disk.Name()) {
					log.Warnf("Device %s has an active partition and filesystem!!!!\n", k)
				}
			}
		}
	}

	return nil
}

// readCfgFile reads a config file and return its contents.
func (r *TrafficOpsReq) readCfgFile(cfg *ConfigFile, dir string) ([]byte, error) {
	var data []byte
	var fullFileName string
	if dir == "" {
		fullFileName = cfg.Path
	} else {
		fullFileName = dir + "/" + cfg.Name
	}

	info, err := os.Stat(fullFileName)
	if err != nil {
		return nil, err
	}
	size := info.Size()

	fd, err := os.Open(fullFileName)
	if err != nil {
		return nil, err
	}
	data = make([]byte, size)
	c, err := fd.Read(data)
	if err != nil || int64(c) != size {
		return nil, errors.New("unable to completely read from '" + cfg.Name + "': " + err.Error())
	}
	fd.Close()

	return data, nil
}

const configFileTempSuffix = `.tmp`

// replaceCfgFile replaces an ATS configuration file with one from Traffic Ops.
func (r *TrafficOpsReq) replaceCfgFile(cfg *ConfigFile) (*FileRestartData, error) {
	if r.Cfg.ReportOnly ||
		(r.Cfg.Files != t3cutil.ApplyFilesFlagAll && r.Cfg.Files != t3cutil.ApplyFilesFlagReval) {
		log.Infof("You elected not to replace %s with the version from Traffic Ops.\n", cfg.Name)
		cfg.ChangeApplied = false
		return &FileRestartData{Name: cfg.Name}, nil
	}

	tmpFileName := cfg.Path + configFileTempSuffix
	log.Infof("Writing temp file '%s' with file mode: '%#o' \n", tmpFileName, cfg.Perm)

	// write a new file, then move to the real location
	// because moving is atomic but writing is not.
	// If we just wrote to the real location and the app or OS or anything crashed,
	// we'd end up with malformed files.

	if _, err := util.WriteFileWithOwner(tmpFileName, cfg.Body, &cfg.Uid, &cfg.Gid, cfg.Perm); err != nil {
		return &FileRestartData{Name: cfg.Name}, errors.New("Failed to write temp config file '" + tmpFileName + "': " + err.Error())
	}

	log.Infof("Copying temp file '%s' to real '%s'\n", tmpFileName, cfg.Path)
	if err := os.Rename(tmpFileName, cfg.Path); err != nil {
		return &FileRestartData{Name: cfg.Name}, errors.New("Failed to move temp '" + tmpFileName + "' to real '" + cfg.Path + "': " + err.Error())
	}
	cfg.ChangeApplied = true
	r.changedFiles = append(r.changedFiles, cfg.Path)

	remapConfigReload := cfg.RemapPluginConfig ||
		cfg.Name == "remap.config" ||
		strings.HasPrefix(cfg.Name, "bg_fetch") ||
		strings.HasPrefix(cfg.Name, "hdr_rw_") ||
		strings.HasPrefix(cfg.Name, "regex_remap_") ||
		strings.HasPrefix(cfg.Name, "set_dscp_") ||
		strings.HasPrefix(cfg.Name, "url_sig_") ||
		strings.HasPrefix(cfg.Name, "uri_signing") ||
		strings.HasSuffix(cfg.Name, ".lua")

	trafficCtlReload := strings.HasSuffix(cfg.Dir, "trafficserver") ||
		remapConfigReload ||
		cfg.Name == "ssl_multicert.config" ||
		cfg.Name == "records.config" ||
		(strings.HasSuffix(cfg.Dir, "ssl") && strings.HasSuffix(cfg.Name, ".cer")) ||
		(strings.HasSuffix(cfg.Dir, "ssl") && strings.HasSuffix(cfg.Name, ".key"))

	trafficServerRestart := cfg.Name == "plugin.config"
	ntpdRestart := cfg.Name == "ntpd.conf"
	sysCtlReload := cfg.Name == "sysctl.conf"

	log.Debugf("Reload state after %s: remap.config: %t reload: %t restart: %t ntpd: %t sysctl: %t", cfg.Name, remapConfigReload, trafficCtlReload, trafficServerRestart, ntpdRestart, sysCtlReload)

	log.Debugf("Setting change applied for '%s'\n", cfg.Name)
	return &FileRestartData{
		Name: cfg.Name,
		RestartData: RestartData{
			TrafficCtlReload:     trafficCtlReload,
			SysCtlReload:         sysCtlReload,
			NtpdRestart:          ntpdRestart,
			TrafficServerRestart: trafficServerRestart,
			RemapConfigReload:    remapConfigReload,
		},
	}, nil
}

// CheckSystemServices is used to verify that packages installed
// are enabled for startup.
func (r *TrafficOpsReq) CheckSystemServices() error {

	if r.Cfg.ServiceAction != t3cutil.ApplyServiceActionFlagRestart { // --service-action=restart ではない場合
		return nil
	}

	result, err := getChkconfig(r.Cfg) // t3c-request --get-data=chkconfig
	if err != nil {
		log.Errorln(err)
		return err
	}

	for ii := range result {
		name := result[ii]["name"]
		value := result[ii]["value"]
		arrv := strings.Fields(value)
		level := []string{}
		enabled := false
		for jj := range arrv {
			// 「3:on」のように左にレベル、右に「on」といった文字列が入る模様
			nv := strings.Split(arrv[jj], ":")
			// onが指定されていたら、ランレベルをlevelに、有効にするかどうかのフラグをenabledに保存します。
			if len(nv) == 2 && strings.Contains(nv[1], "on") {
				level = append(level, nv[0])
				enabled = true
			}
		}
		if !enabled {
			continue
		}

		// SystemDかSystemVかでsystemctlかchkconfigかのコマンドを分離する。
		// systemctl enable <pkg> や chkconfig --level <level> <pkg> onのサービス開始コマンドを実行する
		if r.Cfg.SvcManagement == config.SystemD {

			out, rc, err := util.ExecCommand("/bin/systemctl", "enable", name)
			if err != nil {
				log.Errorf(string(out))
				return errors.New("Unable to enable service " + name + ": " + err.Error())
			}

			if rc == 0 {
				log.Infof("The %s service has been enabled\n", name)
			}

		} else if r.Cfg.SvcManagement == config.SystemV {

			levelValue := strings.Join(level, "")
			_, rc, err := util.ExecCommand("/bin/chkconfig", "--level", levelValue, name, "on")
			if err != nil {
				return errors.New("Unable to enable service " + name + ": " + err.Error())
			}

			if rc == 0 {
				log.Infof("The %s service has been enabled\n", name)
			}

		} else {
			log.Errorf("Unable to ensure %s service is enabled, SvcMananagement type is %s\n", name, r.Cfg.SvcManagement)
		}
	}

	return nil
}

// IsPackageInstalled returns true/false if the named rpm package is installed.
// the prefix before the version is matched.
func (r *TrafficOpsReq) IsPackageInstalled(name string) bool {
	for k, v := range r.pkgs {
		if strings.HasPrefix(k, name) {
			return v
		}
	}

	log.Infof("IsPackageInstalled '%v' not found in cache, querying rpm", name)
	pkgArr, err := util.PackageInfo("pkg-query", name)
	if err != nil {
		log.Errorf(`IsPackageInstalled PackageInfo(pkg-query, %v) failed, caching as not installed and returning false! Error: %v\n`, name, err.Error())
		r.pkgs[name] = false
		return false
	}

	if len(pkgArr) > 0 {
		pkgAndVersion := pkgArr[0]
		log.Infof("IsPackageInstalled '%v' found in rpm, adding '%v' to cache", name, pkgAndVersion)
		r.pkgs[pkgAndVersion] = true
		return true
	}

	log.Infof("IsPackageInstalled '%v' not found in rpm, adding '%v'=false to cache", name, name)
	r.pkgs[name] = false
	return false
}

// GetConfigFile fetchs a 'Configfile' by file name.
func (r *TrafficOpsReq) GetConfigFile(name string) (*ConfigFile, bool) {
	cfg, ok := r.configFiles[name]
	return cfg, ok
}

// GetConfigFileList fetches and parses the multipart config files
// for a cache from traffic ops and loads them into the configFiles map.
func (r *TrafficOpsReq) GetConfigFileList() error {

	var atsUid int = 0
	var atsGid int = 0

	// trafficserverの設定として指定された「ats」オーナーであることのチェック。その後にatsのuidやgidを取得する
	atsUser, err := user.Lookup(config.TrafficServerOwner)
	if err != nil {
		log.Errorf("could not lookup the trafficserver, '%s', owner uid, using uid/gid 0",
			config.TrafficServerOwner)
	} else {

		// uidをint型に変換する
		atsUid, err = strconv.Atoi(atsUser.Uid)
		if err != nil {
			log.Errorf("could not parse the ats UID.")
			atsUid = 0
		}

		// gidをint型に変換する
		atsGid, err = strconv.Atoi(atsUser.Gid)
		if err != nil {
			log.Errorf("could not parse the ats GID.")
			atsUid = 0
		}
	}

	// t3c-generateによるTrafficOpsから設定情報を取得しての設定生成処理はここで行われます。
	allFiles, err := generate(r.Cfg)
	if err != nil {
		return errors.New("requesting data generating config files: " + err.Error())
	}

	r.configFiles = map[string]*ConfigFile{}
	r.configFileWarnings = map[string][]string{}
	var mode os.FileMode

	// generateで取得した情報を全てconfigFilesのオブジェクトにマッピングします。このオブジェクトはファイル名、パス、ファイル内容、Uid、Gid、パーミッション等を含みます。
	for _, file := range allFiles {

		if file.Secure {
			mode = 0600
		} else {
			mode = 0644
		}

		// ファイル情報をConfigFile構造体に格納する
		r.configFiles[file.Name] = &ConfigFile{
			Name:     file.Name,
			Path:     filepath.Join(file.Path, file.Name),
			Dir:      file.Path,
			Body:     []byte(file.Text),
			Uid:      atsUid,
			Gid:      atsGid,
			Perm:     mode,
			Warnings: file.Warnings,
		}

		// warningがあれば登録しておく。ここはmainから最後にprintされる内容になります。
		for _, warn := range file.Warnings {

			// 警告がなければそのままcontinueする
			if warn == "" {
				continue
			}

			// 警告があればr.configFileWarningsに登録しておく
			r.configFileWarnings[file.Name] = append(r.configFileWarnings[file.Name], warn)
		}
	}

	return nil
}

func (r *TrafficOpsReq) PrintWarnings() {
	log.Infoln("======== Summary of config warnings that may need attention. ========")
	for file, warning := range r.configFileWarnings {
		for _, warning := range warning {
			log.Warnf("%s: %s", file, warning)
		}
	}
	log.Infoln("======== End warning summary ========")
}

// CheckRevalidateState retrieves and returns the revalidate status from Traffic Ops.
func (r *TrafficOpsReq) CheckRevalidateState(sleepOverride bool) (UpdateStatus, error) {
	log.Infoln("Checking revalidate state.")

	// 関数の第１引数(sleepOverride)にfalseが指定され、かつ revalの対象外の場合には即座にreturnする
	if !sleepOverride &&
		(r.Cfg.ReportOnly || r.Cfg.Files != t3cutil.ApplyFilesFlagReval) { // --report-only=true または 「--files=reval以外」
		updateStatus := UpdateTropsNotNeeded
		log.Infof("CheckRevalidateState returning %v\n", updateStatus)
		return updateStatus, nil
	}

	updateStatus := UpdateTropsNotNeeded

	// 下記ではt3c-request --get-data=update-status が実行される
	// see: https://traffic-control-cdn.readthedocs.io/en/latest/api/v4/servers_hostname_update_status.html
	serverStatus, err := getUpdateStatus(r.Cfg)
	if err != nil {
		log.Errorln("getting update status: " + err.Error())
		return UpdateTropsNotNeeded, errors.New("getting update status: " + err.Error())
	}

	log.Infof("my status: %s\n", serverStatus.Status)

	// APIのjsonレスポンスの戻り値として `use_reval_pending=false` が含まれている場合
	if serverStatus.UseRevalPending == false {
		log.Errorln("Update URL: Instant invalidate is not enabled.  Separated revalidation requires upgrading to Traffic Ops version 2.2 and enabling this feature.")
		return UpdateTropsNotNeeded, nil
	}

	// APIのjsonレスポンスの戻り値として `reval_pending=true` が含まれている場合
	if serverStatus.RevalPending == true {
		log.Errorln("Traffic Ops is signaling that a revalidation is waiting to be applied.")
		updateStatus = UpdateTropsNeeded
		if serverStatus.ParentRevalPending == true { // `parent_reval_pending=true`が含まれている場合
			if r.Cfg.WaitForParents {
				log.Infoln("Traffic Ops is signaling that my parents need to revalidate, not revalidating.")
				updateStatus = UpdateTropsNotNeeded
			} else {
				log.Infoln("Traffic Ops is signaling that my parents need to revalidate, but wait-for-parents is false, revalidating anyway.")
			}
		}
	} else if serverStatus.RevalPending == false && !r.Cfg.ReportOnly && r.Cfg.Files == t3cutil.ApplyFilesFlagReval {
		// `reval_pending=false` かつ `--report-only=false` かつ `--files=reval` の場合 には更新しない
		log.Errorln("In revalidate mode, but no update needs to be applied. I'm outta here.")
		return UpdateTropsNotNeeded, nil
	} else {
		log.Errorln("Traffic Ops is signaling that no revalidations are waiting to be applied.")
		return UpdateTropsNotNeeded, nil
	}

	// /var/lib/trafficcontrol-cache-config/status/に存在するステータスファイル(REPORTED等)のステータスに変更があれば該当ステータスのファイルを作成する。古いステータスファイルは削除する。
	err = r.checkStatusFiles(serverStatus.Status)
	if err != nil {
		log.Errorln(errors.New("checking status files: " + err.Error()))
	} else {
		log.Infoln("CheckRevalidateState checkStatusFiles returned nil error")
	}

	log.Infof("CheckRevalidateState returning %v\n", updateStatus)
	return updateStatus, nil
}

// CheckSYncDSState retrieves and returns the DS Update status from Traffic Ops.
// 「--files=reval」の場合にはこのロジックのif文のメイン処理は通らないので「--files=all」の時だけだと思われる。
func (r *TrafficOpsReq) CheckSyncDSState() (UpdateStatus, error) {

	updateStatus := UpdateTropsNotNeeded
	randDispSec := time.Duration(0)
	log.Debugln("Checking syncds state.")

	//	if r.Cfg.RunMode == t3cutil.ModeSyncDS || r.Cfg.RunMode == t3cutil.ModeBadAss || r.Cfg.RunMode == t3cutil.ModeReport
	if r.Cfg.Files != t3cutil.ApplyFilesFlagReval { // 「--files=revalでない値」が指定された場合(関数呼び出しの手前でもチェックされるが、関数の中でもチェックされる)

		// t3c-request --get-data=update-status を実行してサーバのステータス情報を取得します
		// serverStatusオブジェクトには下記APIのレスポンスが格納されます。
		//   See: https://traffic-control-cdn.readthedocs.io/en/latest/api/v4/servers_hostname_update_status.html
		serverStatus, err := getUpdateStatus(r.Cfg)
		if err != nil {
			log.Errorln("getting '" + r.Cfg.CacheHostName + "' update status: " + err.Error())
			return updateStatus, err
		}

		// APIレスポンスの`upd_pending=true`の値によって処理を分岐する。
		if serverStatus.UpdatePending {
			updateStatus = UpdateTropsNeeded
			log.Errorln("Traffic Ops is signaling that an update is waiting to be applied")

			// 取得したレスポンスで 「parent_pending=true」 かつ オプションに「--wait-for-parents=true」 が指定されている場合には、parentが更新されたことを待つ
			if serverStatus.ParentPending && r.Cfg.WaitForParents {
				log.Errorln("Traffic Ops is signaling that my parents need an update.")
				// TODO should reval really not sleep?
				// 「--report-only=false」 かつ 「--files=revalでない値」 が指定された場合 (--files=revalのチェックは呼び出し元でチェックしているがここでも実施している)
				if !r.Cfg.ReportOnly && r.Cfg.Files != t3cutil.ApplyFilesFlagReval {
					log.Infof("sleeping for %ds to see if the update my parents need is cleared.", randDispSec/time.Second)
					serverStatus, err = getUpdateStatus(r.Cfg)
					if err != nil {
						return updateStatus, err
					}

					// APIレスポンスが`parent_pending=true` または `parent_reval_pending=true`の場合には、parent側の処理がまだ完了していないということでまだ処理を実施しない
					if serverStatus.ParentPending || serverStatus.ParentRevalPending {
						log.Errorln("My parents still need an update, bailing.")
						return UpdateTropsNotNeeded, nil
					} else {
						log.Debugln("The update on my parents cleared; continuing.")
					}
				}
			} else {
				log.Debugf("Processing with update: Traffic Ops server status %+v config wait-for-parents %+v", serverStatus, r.Cfg.WaitForParents)
			}
		} else if !r.Cfg.IgnoreUpdateFlag { // `upd_pending=false` かつ --ignore-update-flag=false が指定された場合
			log.Errorln("no queued update needs to be applied.  Running revalidation before exiting.")
			r.RevalidateWhileSleeping()
			return UpdateTropsNotNeeded, nil
		} else {
			log.Errorln("Traffic Ops is signaling that no update is waiting to be applied.")
		}

		// check local status files.
		err = r.checkStatusFiles(serverStatus.Status)
		if err != nil {
			log.Errorln(err)
		}
	}

	return updateStatus, nil
}

// CheckReloadRestart determines the final reload/restart state after all config files are processed.
func (r *TrafficOpsReq) CheckReloadRestart(data []FileRestartData) RestartData {
	rd := RestartData{}
	for _, changedFile := range data {
		rd.TrafficCtlReload = rd.TrafficCtlReload || changedFile.TrafficCtlReload
		rd.SysCtlReload = rd.SysCtlReload || changedFile.SysCtlReload
		rd.NtpdRestart = rd.NtpdRestart || changedFile.NtpdRestart
		rd.TeakdRestart = rd.TeakdRestart || changedFile.TeakdRestart
		rd.TrafficServerRestart = rd.TrafficServerRestart || changedFile.TrafficServerRestart
		rd.RemapConfigReload = rd.RemapConfigReload || changedFile.RemapConfigReload
	}
	return rd
}

// ProcessConfigFiles processes all config files retrieved from Traffic Ops.
func (r *TrafficOpsReq) ProcessConfigFiles() (UpdateStatus, error) {
	var updateStatus UpdateStatus = UpdateTropsNotNeeded

	log.Infoln(" ======== Start processing config files ========")

	filesAdding := []string{} // list of file names being added, needed for verification.
	for fileName, _ := range r.configFiles {
		filesAdding = append(filesAdding, fileName)
	}

	// r.configFilesはmainのtrops.GetConfigFileList()にてオブジェクト内容が登録される。TrafficOpsから取得・生成したファイルパス情報が含まれている
	for _, cfg := range r.configFiles {
		// add service metadata
		// ファイルパスに含まれる情報からどのサービスかを判断してcfg.Serviceに値を設定する。trafficserver, puppet, system ntpd, unknownがある。 ログへの出力にしか使われてなさそう。
		if strings.Contains(cfg.Path, "/opt/trafficserver/") || strings.Contains(cfg.Dir, "udev") {
			cfg.Service = "trafficserver"
			if !r.Cfg.InstallPackages && !r.IsPackageInstalled("trafficserver") {
				log.Errorln("Not installing packages, but trafficserver isn't installed. Continuing.")
			}
		} else if strings.Contains(cfg.Path, "/opt/ort") && strings.Contains(cfg.Name, "12M_facts") {
			cfg.Service = "puppet"
		} else if strings.Contains(cfg.Path, "cron") || strings.Contains(cfg.Name, "sysctl.conf") || strings.Contains(cfg.Name, "50-ats.rules") || strings.Contains(cfg.Name, "cron") {
			cfg.Service = "system"
		} else if strings.Contains(cfg.Path, "ntp.conf") {
			cfg.Service = "ntpd"
		} else {
			cfg.Service = "unknown"
		}

		log.Debugf("About to process config file: %s, service: %s\n", cfg.Path, cfg.Service)

		err := r.checkConfigFile(cfg, filesAdding)
		if err != nil {
			log.Errorln(err)
		}
	}

	changesRequired := 0
	shouldRestartReload := ShouldReloadRestart{[]FileRestartData{}}

	for _, cfg := range r.configFiles {
		if cfg.ChangeNeeded &&
			!cfg.ChangeApplied &&
			cfg.AuditComplete &&
			!cfg.PreReqFailed &&
			!cfg.AuditFailed {

			changesRequired++
			if cfg.Name == "plugin.config" && r.configFiles["remap.config"].PreReqFailed == true {
				updateStatus = UpdateTropsFailed
				log.Errorln("plugin.config changed however, prereqs failed for remap.config so I am skipping updates for plugin.config")
				continue
			} else if cfg.Name == "remap.config" && r.configFiles["plugin.config"].PreReqFailed == true {
				updateStatus = UpdateTropsFailed
				log.Errorln("remap.config changed however, prereqs failed for plugin.config so I am skipping updates for remap.config")
				continue
			} else if cfg.Name == "ip_allow.config" && !r.Cfg.UpdateIPAllow {
				log.Warnln("ip_allow.config changed, not updating! Run with --mode=badass or --syncds-updates-ipallow=true to update!")
				continue
			} else {
				log.Debugf("All Prereqs passed for replacing %s on disk with that in Traffic Ops.\n", cfg.Name)
				reData, err := r.replaceCfgFile(cfg)
				if err != nil {
					log.Errorf("failed to replace the config file, '%s',  on disk with data in Traffic Ops.\n", cfg.Name)
				}
				shouldRestartReload.ReloadRestart = append(shouldRestartReload.ReloadRestart, *reData)
			}
		}
	}

	r.RestartData = r.CheckReloadRestart(shouldRestartReload.ReloadRestart)

	if 0 < len(r.changedFiles) {
		log.Infof("Final state: remap.config: %t reload: %t restart: %t ntpd: %t sysctl: %t", r.RemapConfigReload, r.TrafficCtlReload, r.TrafficServerRestart, r.NtpdRestart, r.SysCtlReload)
	}

	if updateStatus != UpdateTropsFailed && changesRequired > 0 {
		return UpdateTropsNeeded, nil
	}

	return updateStatus, nil
}

// ProcessPackages retrieves a list of required RPM's from Traffic Ops
// and determines which need to be installed or removed on the cache.
func (r *TrafficOpsReq) ProcessPackages() error {
	log.Infoln("Calling ProcessPackages")
	// get the package list for this cache from Traffic Ops. 
	// t3c-request --get-data=packagesの実行してTrafficOpsからこのサーバで取得するパッケージリストを取得する
	pkgs, err := getPackages(r.Cfg)
	if err != nil {
		return errors.New("getting packages: " + err.Error())
	}
	log.Infof("ProcessPackages got %+v\n", pkgs)

	var install []string   // install package list.
	var uninstall []string // uninstall package list
	// loop through the package list to build an install and uninstall list.
	// t3c-request --get-data=packagesのレスポンスで取得したpkgsに対してrangeでイテレーションする
	for ii := range pkgs {

		var instpkg string // installed package
		var reqpkg string  // required package
		log.Infof("Processing package %s-%s\n", pkgs[ii].Name, pkgs[ii].Version)

		// インストール済みパッケージかどうかをrpmコマンドで確認する。インストール済みならば戻り値のarrに格納される。
		arr, err := util.PackageInfo("pkg-query", pkgs[ii].Name)
		if err != nil {
			return errors.New("PackgeInfo pkg-query: " + err.Error())
		}

		// go needs the ternary operator :)
		// インストール済みかどうかを判定し、インストール済みならinstpkg変数にパッケージ名を格納する
		// arrは1以上は存在することがない。なぜなら、このコードパスのロジックは range pkgsで処理されているので1つのパッケージ毎にしかイテレーションしないため。
		if len(arr) == 1 {
			instpkg = arr[0]
		} else {
			instpkg = ""
		}

		// check if the full package version is installed
		// 取得したパッケージ名とバージョンを合わせて変数名を構成する。この変数に入った<パッケージ>+<バージョン>の文字列の値と先ほどrpmで取得したインストール済みの文字列を比較することによって、インストールされているか、更新が必要かの判断を行う。
		fullPackage := pkgs[ii].Name + "-" + pkgs[ii].Version

		// --install-packages=trueの場合
		if r.Cfg.InstallPackages {

			if instpkg == fullPackage {

				// rpmでのパッケージ取得結果とTrafficOpsで取得したパッケージがバージョンも含めて一致する場合
				log.Infof("%s Currently installed and not marked for removal\n", reqpkg)
				r.pkgs[fullPackage] = true
				continue

			} else if instpkg != "" { // the installed package needs upgrading.

				// rpmで該当パッケージが取得できたが、TrafficOpsで取得したパッケージがバージョンも含めて一致しない場合には更新対象と判断する
				// 古いバージョンのパッケージは削除対象としてuninstallにappendされる
				log.Infof("%s Currently installed and marked for removal\n", instpkg)
				uninstall = append(uninstall, instpkg)

				// the required package needs installing.
				// 新しいバージョンのパッケージはインストール対象としてinstallにappendされる
				log.Infof("%s is Not installed and is marked for installation.\n", fullPackage)
				install = append(install, fullPackage)

				// get a list of packages that depend on this one and mark dependencies
				// for deletion.
				// pkg-requiresにより、「rpm -q --whatrequires」により既に依存しているパッケージがあるとのことなのでインストール不要であることがわかる。
				// この場合にはインストール対象に含めない
				arr, err = util.PackageInfo("pkg-requires", instpkg)
				if err != nil {
					return errors.New("PackgeInfo pkg-requires: " + err.Error())
				}

				// 「rpm -q --whatrequires」で1件以上でもひっかかればそのパッケージはすでに利用されていることになるので、インストールしないようにする。
				// TODO: ただ、この場合には、すでに 「if instpkg == fullPackage」の後のelse ifの処理なので指定されたバージョンのパッケージが入っているわけではないと思うが問題ないのか?
				if len(arr) > 0 {
					for jj := range arr {
						log.Infof("%s is Currently installed and depends on %s and needs to be removed.", arr[jj], instpkg)
						uninstall = append(uninstall, arr[jj])
					}
				}

			} else { 
				// 「instpkg == ""」の場合にこのelseの分岐に入る。この場合にはシステムに該当パッケージがインストールされていないことを意味しているため、パッケージがインストール対象として追加される。
				// the required package needs installing.
				log.Infof("%s is Not installed and is marked for installation.\n", fullPackage)
				log.Errorf("%s is Not installed and is marked for installation.\n", fullPackage)
				install = append(install, fullPackage)
			}

		} else { // --install-packages=falseの場合にはインストールはされない。ただログを出すだけ

			// Only check if packages exist and complain if they are wrong.
			if instpkg == fullPackage {
				// 既にパッケージの対象バージョンがインストールされている場合
				log.Infof("%s Currently installed.\n", reqpkg)
				r.pkgs[fullPackage] = true
				continue
			} else if instpkg != "" { // the installed package needs upgrading.
				// パッケージのアップデートが必要な場合
				log.Errorf("%s Wrong version currently installed.\n", instpkg)
				r.pkgs[instpkg] = true
			} else {
				// システムにパッケージがインストールされていない場合
				// the required package needs installing.
				log.Errorf("%s is Not installed.\n", fullPackage)
			}
		}
	}

	log.Debugf("number of packages requiring installation: %d\n", len(install))
	if r.Cfg.ReportOnly {
		log.Errorf("number of packages requiring installation: %d\n", len(install))
	}

	log.Debugf("number of packages requiring removal: %d\n", len(uninstall))
	if r.Cfg.ReportOnly {
		log.Errorf("number of packages requiring removal: %d\n", len(uninstall))
	}

	// --install-packages=trueの場合
	if r.Cfg.InstallPackages {

		// インストールした数
		log.Debugf("number of packages requiring installation: %d\n", len(install))
		if r.Cfg.ReportOnly {
			log.Errorf("number of packages requiring installation: %d\n", len(install))
		}

		// 依存でパッケージがインストールされていたので、インストールしなかった対象の数
		log.Debugf("number of packages requiring removal: %d\n", len(uninstall))
		if r.Cfg.ReportOnly {
			log.Errorf("number of packages requiring removal: %d\n", len(uninstall))
		}

		// インストール数が1件以上でも存在する場合
		if len(install) > 0 {
			for ii := range install {
				result, err := util.PackageAction("info", install[ii])    // 指定されたパッケージのyum infoを実施し、失敗したらエラーにする
				if err != nil || result != true {
					return errors.New("Package " + install[ii] + " is not available to install: " + err.Error())
				}
			}
			log.Infoln("All packages available.. proceding..")

			// uninstall packages marked for removal
			if len(install) > 0 && r.Cfg.InstallPackages {                // --install-packages=trueの場合
				for jj := range uninstall {
					log.Infof("Uninstalling %s\n", uninstall[jj])
					r, err := util.PackageAction("remove", uninstall[jj]) // 指定されたパッケージのyum removeを実施する
					if err != nil {
						// パッケージのuninstallに失敗した場合
						return errors.New("Unable to uninstall " + uninstall[jj] + " : " + err.Error())
					} else if r == true {
						// パッケージのuninstallに成功した場合
						log.Infof("Package %s was uninstalled\n", uninstall[jj])
					}
				}

				// install the required packages
				for jj := range install {
					pkg := install[jj]
					log.Infof("Installing %s\n", pkg)
					result, err := util.PackageAction("install", pkg)  // 指定されたパッケージのyum installを実施する
					if err != nil {
						return errors.New("Unable to install " + pkg + " : " + err.Error())
					} else if result == true {
						r.pkgs[pkg] = true
						r.installedPkgs[pkg] = struct{}{}
						log.Infof("Package %s was installed\n", pkg)
					}
				}
			}
		}

		// --report-only=trueの場合には、インストール対象を表示だけして終了する
		if r.Cfg.ReportOnly && len(install) > 0 {
			for ii := range install {
				log.Errorf("\nIn Report mode and %s needs installation.\n", install[ii])
				return errors.New("In Report mode and packages need installation")
			}
		}
	}
	return nil
}

func (r *TrafficOpsReq) RevalidateWhileSleeping() (UpdateStatus, error) {

	updateStatus, err := r.CheckRevalidateState(true)
	if err != nil {
		return updateStatus, err
	}

	// UpdateTropsNotNeeded = 0なので、それ以外のケースだと下記が実行される。
	if updateStatus != 0 {
		r.Cfg.Files = t3cutil.ApplyFilesFlagReval
		// TODO verify? This is for revalidating after a syncds, so we probably do want to wait for parents here, and users probably don't for the main syncds run. But, this feels surprising.
		// The better solution is to gut the RevalidateWhileSleeping stuff, once TO can handle more load
		r.Cfg.WaitForParents = true

		err = r.GetConfigFileList()
		if err != nil {
			return updateStatus, err
		}

		updateStatus, err := r.ProcessConfigFiles()
		if err != nil {
			return updateStatus, err
		}

		if err := r.StartServices(&updateStatus); err != nil {
			return updateStatus, errors.New("failed to start services: " + err.Error())
		}

		if err := r.UpdateTrafficOps(&updateStatus); err != nil {
			log.Errorf("failed to update Traffic Ops: %s\n", err.Error())
		}

		r.TrafficCtlReload = false
	}

	return updateStatus, nil
}

// StartServices reloads, restarts, or starts ATS as necessary,
// according to the changed config files and run mode.
// Returns nil on success or any error.
func (r *TrafficOpsReq) StartServices(syncdsUpdate *UpdateStatus) error {
	serviceNeeds := t3cutil.ServiceNeedsNothing

	if r.Cfg.ServiceAction == t3cutil.ApplyServiceActionFlagRestart { // --service-action=restart
		// --service-action=restartの場合には、再起動させるようにする
		serviceNeeds = t3cutil.ServiceNeedsRestart
	} else {
		// --service-action=restart以外の場合にはt3c-check-reloadを実行して、次回の状態をどうするか決める(何もしない、再起動、再読込、不正の4種類)
		err := error(nil)
		if serviceNeeds, err = checkReload(r.changedFiles); err != nil {
			return errors.New("determining if service needs restarted - not reloading or restarting! : " + err.Error())
		}
	}

	log.Infof("t3c-check-reload returned '%+v'\n", serviceNeeds)

	// We have our own internal knowledge of files that have been modified as well
	// If check-reload does not know about these and we do, then we should initiate
	// a reload as well
	// 再起動も再読み込みいずれも指定されていないが、r.TrafficCtlReloadかr.RemapConfigReloadが内部状態として指定されている場合には再読み込みとして扱うことにする
	if serviceNeeds != t3cutil.ServiceNeedsRestart && serviceNeeds != t3cutil.ServiceNeedsReload {
		if r.TrafficCtlReload || r.RemapConfigReload {
			log.Infof("ATS config files unchanged, we updated files via t3c-apply, ATS needs reload")
			serviceNeeds = t3cutil.ServiceNeedsReload
		}
	}

	// 再起動か再読込のいずれかが指定されているにもかかわらず、trafficserverがインストールされていなければエラーとする。
	if (serviceNeeds == t3cutil.ServiceNeedsRestart || serviceNeeds == t3cutil.ServiceNeedsReload) && !r.IsPackageInstalled("trafficserver") {
		// TODO try to reload/restart anyway? To allow non-RPM installs?
		return errors.New("trafficserver needs " + serviceNeeds.String() + " but is not installed.")
	}

	// 「/usr/sbin/service trafficserver status」を実行してActiveが帰ってきているかによって、サービス状態を判定する。
	svcStatus, _, err := util.GetServiceStatus("trafficserver")
	if err != nil {
		return errors.New("getting trafficserver service status: " + err.Error())
	}

	if r.Cfg.ReportOnly {  // --report-only=trueが指定された場合

		if serviceNeeds == t3cutil.ServiceNeedsRestart {
			log.Errorln("ATS configuration has changed.  The new config will be picked up the next time ATS is started.")
		} else if serviceNeeds == t3cutil.ServiceNeedsReload {
			log.Errorln("ATS configuration has changed. 'traffic_ctl config reload' needs to be run")
		}
		return nil

	} else if r.Cfg.ServiceAction == t3cutil.ApplyServiceActionFlagRestart { // --service-action=restart が指定されている場合

		// デフォルトは「restart」
		startStr := "restart"

		// サービスが起動していなければ「start」オプションを指定
		if svcStatus != util.SvcRunning {
			startStr = "start"
		}

		// ここでtrafficserverサービスのstartやrestartが行われる
		if _, err := util.ServiceStart("trafficserver", startStr); err != nil {
			return errors.New("failed to restart trafficserver")
		}
		log.Infoln("trafficserver has been " + startStr + "ed")

		// syncdsUpdate中の「UpdateTropsNeeded」の値は「UpdateTropsSuccessful」に変更する
		if *syncdsUpdate == UpdateTropsNeeded {
			*syncdsUpdate = UpdateTropsSuccessful
		}

		return nil // we restarted, so no need to reload

	} else if r.Cfg.ServiceAction == t3cutil.ApplyServiceActionFlagReload { // 「--service-action=reload」が指定された場合

		if serviceNeeds == t3cutil.ServiceNeedsRestart {

			// reload(--service-action=reload)オプションが指定しているにもかかわらず、サービスとしてはrestartを必要とする場合にはエラーログを吐いておく

			// syncdsUpdate中の「UpdateTropsNeeded」の値は「UpdateTropsSuccessful」に変更する
			if *syncdsUpdate == UpdateTropsNeeded {
				*syncdsUpdate = UpdateTropsSuccessful
			}
			log.Errorln("ATS configuration has changed.  The new config will be picked up the next time ATS is started.")

		} else if serviceNeeds == t3cutil.ServiceNeedsReload {

			log.Infoln("ATS configuration has changed, Running 'traffic_ctl config reload' now.")

			// 「traffic_ctl config reload」が実行される
			if _, _, err := util.ExecCommand(config.TSHome+config.TrafficCtl, "config", "reload"); err != nil {

				if *syncdsUpdate == UpdateTropsNeeded {
					*syncdsUpdate = UpdateTropsFailed
				}

				return errors.New("ATS configuration has changed and 'traffic_ctl config reload' failed, check ATS logs: " + err.Error())
			}

			// syncdsUpdate中の「UpdateTropsNeeded」の値は「UpdateTropsSuccessful」に変更する
			if *syncdsUpdate == UpdateTropsNeeded {
				*syncdsUpdate = UpdateTropsSuccessful
			}

			log.Infoln("ATS 'traffic_ctl config reload' was successful")
		}

		// syncdsUpdate中の「UpdateTropsNeeded」の値は「UpdateTropsSuccessful」に変更する
		if *syncdsUpdate == UpdateTropsNeeded {
			*syncdsUpdate = UpdateTropsSuccessful
		}

		return nil
	}

	return nil
}

// 関数の引数で更新後のステータスを受け取り、「t3c-request --get-data=update-status」の結果を再取得して取得ステータスと実際の処理で乖離していたらログを出す。
// その後、t3c applyにより設定が更新された場合にはsendUpdate()によってt3c-updateが実行され、TrafficOps APIへのステータスの更新リクエストされます。
func (r *TrafficOpsReq) UpdateTrafficOps(syncdsUpdate *UpdateStatus) error {
	var performUpdate bool

	// t3c-request --get-data=update-statusを実行して更新後のステータスを取得する
	serverStatus, err := getUpdateStatus(r.Cfg)
	if err != nil {
		return errors.New("failed to update Traffic Ops: " + err.Error())
	}

	if *syncdsUpdate == UpdateTropsNotNeeded && (serverStatus.UpdatePending == true || serverStatus.RevalPending == true) {
		// case1: TrafficOpsで更新不要と判断(UpdateTropsNotNeeded) かつ ( `upd_pending=true` または `reval_pending=true` )の場合
		performUpdate = true
		log.Errorln("Traffic Ops is signaling that an update is ready to be applied but, none was found! Clearing update state in Traffic Ops anyway.")
	} else if *syncdsUpdate == UpdateTropsNotNeeded {
		// case2: TrafficOpsで更新不要と判断(UpdateTropsNotNeeded)
		log.Errorln("Traffic Ops does not require an update at this time")
		return nil
	} else if *syncdsUpdate == UpdateTropsFailed {
		// case3: 更新処理に失敗した場合
		log.Errorln("Traffic Ops requires an update but, applying the update locally failed.  Traffic Ops is not being updated.")
		return nil
	} else if *syncdsUpdate == UpdateTropsSuccessful {
		// case4: 更新処理に成功した場合
		performUpdate = true
		log.Errorln("Traffic Ops requires an update and it was applied successfully.  Clearing update state in Traffic Ops.")
	}

	// 上記のcase1からcase4のどの遷移にも入らなかった場合にこの遷移に入る
	if !performUpdate {
		return nil
	}

	if r.Cfg.ReportOnly {
		log.Errorln("In Report mode and Traffic Ops needs updated you should probably do that manually.")
		return nil
	}

	// TODO: The boolean flags/representation can be removed after ATC (v7.0+)
	// sendUpdate()の中でTrafficOpsに対してserverStatusの更新処理を行う(実際にはt3c-updateが実行される)
	if !r.Cfg.ReportOnly && !r.Cfg.NoUnsetUpdateFlag {  // --report-only=false かつ --no-unset-update-flag=false
		if r.Cfg.Files == t3cutil.ApplyFilesFlagAll { // --files=all
			b := false
			err = sendUpdate(r.Cfg, serverStatus.ConfigUpdateTime, nil, &b, nil)
		} else if r.Cfg.Files == t3cutil.ApplyFilesFlagReval { // --files=reval
			b := false
			err = sendUpdate(r.Cfg, nil, serverStatus.RevalidateUpdateTime, nil, &b)
		}
		if err != nil {
			return errors.New("Traffic Ops Update failed: " + err.Error())
		}
		log.Infoln("Traffic Ops has been updated.")
	}
	return nil
}
