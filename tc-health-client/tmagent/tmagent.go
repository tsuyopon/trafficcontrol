package tmagent

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
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/lib/go-tc"
	"github.com/apache/trafficcontrol/tc-health-client/config"
	"github.com/apache/trafficcontrol/tc-health-client/util"
	"github.com/apache/trafficcontrol/traffic_monitor/tmclient"
	"gopkg.in/yaml.v2"
)

const (
	TrafficCtl     = "traffic_ctl"
	ParentsFile    = "parent.config"
	StrategiesFile = "strategies.yaml"
)

// this global is used to auto select the
// proper ATS traffic_ctl command to use
// when querying host status. for ATS
// version 10 and greater this will remain
// at 0.  For ATS version 9, this will be
// auto updated to 1
var traffic_ctl_index = 0

type ParentAvailable interface {
	available(reasonCode string) bool
}

// the necessary data required to keep track of trafficserver config
// files, lists of parents a trafficserver instance uses, and directory
// locations used for configuration and trafficserver executables.
type ParentInfo struct {
	ParentDotConfig        util.ConfigFile
	StrategiesDotYaml      util.ConfigFile
	TrafficServerBinDir    string
	TrafficServerConfigDir string
	Parents                map[string]ParentStatus
	Cfg                    config.Cfg
}

// when reading the 'strategies.yaml', these fields are used to help
// parse out fail_over objects.
type FailOver struct {
	MaxSimpleRetries      int      `yaml:"max_simple_retries,omitempty"`
	MaxUnavailableRetries int      `yaml:"max_unavailable_retries,omitempty"`
	RingMode              string   `yaml:"ring_mode,omitempty"`
	ResponseCodes         []int    `yaml:"response_codes,omitempty"`
	MarkDownCodes         []int    `yaml:"markdown_codes,omitempty"`
	HealthCheck           []string `yaml:"health_check,omitempty"`
}

// the trafficserver 'HostStatus' fields that are necessary to interface
// with the trafficserver 'traffic_ctl' command.
type ParentStatus struct {
	Fqdn                 string
	ActiveReason         bool
	LocalReason          bool
	ManualReason         bool
	LastTmPoll           int64
	UnavailablePollCount int
	MarkUpPollCount      int
}

// used to get the overall parent availablity from the
// HostStatus markdown reasons.  all markdown reasons
// must be true for a parent to be considered available.
func (p ParentStatus) available(reasonCode string) bool {
	rc := false

	switch reasonCode {
	case "active":
		rc = p.ActiveReason
	case "local":
		rc = p.LocalReason
	case "manual":
		rc = p.ManualReason
	}
	return rc
}

// used to log that a parent's status is either UP or
// DOWN based upon the HostStatus reason codes.  to
// be considered UP, all reason codes must be 'true'.
func (p ParentStatus) Status() string {
	if !p.ActiveReason {
		return "DOWN"
	} else if !p.LocalReason {
		return "DOWN"
	} else if !p.ManualReason {
		return "DOWN"
	}
	return "UP"
}

type StatusReason int

// these are the HostStatus reason codes used withing
// trafficserver.
const (
	ACTIVE StatusReason = iota
	LOCAL
	MANUAL
)

// used for logging a parent's HostStatus reason code
// setting.
func (s StatusReason) String() string {
	switch s {
	case ACTIVE:
		return "ACTIVE"
	case LOCAL:
		return "LOCAL"
	case MANUAL:
		return "MANUAL"
	}
	return "UNDEFINED"
}

// the fields used from 'strategies.yaml' that describe
// a parent.
type Host struct {
	HostName  string     `yaml:"host"`
	Protocols []Protocol `yaml:"protocol"`
}

// the protocol object in 'strategies.yaml' that help to
// describe a parent.
type Protocol struct {
	Scheme           string  `yaml:"scheme"`
	Port             int     `yaml:"port"`
	Health_check_url string  `yaml:"health_check_url,omitempty"`
	Weight           float64 `yaml:"weight,omitempty"`
}

// a trafficserver strategy object from 'strategies.yaml'.
type Strategy struct {
	Strategy        string   `yaml:"strategy"`
	Policy          string   `yaml:"policy"`
	HashKey         string   `yaml:"hash_key,omitempty"`
	GoDirect        bool     `yaml:"go_direct,omitempty"`
	ParentIsProxy   bool     `yaml:"parent_is_proxy,omitempty"`
	CachePeerResult bool     `yaml:"cache_peer_result,omitempty"`
	Scheme          string   `yaml:"scheme"`
	FailOvers       FailOver `yaml:"failover,omitempty"`
}

// the top level array defintions in a trafficserver 'strategies.yaml'
// configuration file.
type Strategies struct {
	Strategy []Strategy    `yaml:"strategies"`
	Hosts    []Host        `yaml:"hosts"`
	Groups   []interface{} `yaml:"groups"`
}

// used at startup to load a trafficservers list of parents from
// it's 'parent.config', 'strategies.yaml' and current parent
// status from trafficservers HostStatus subsystem.
func NewParentInfo(cfg config.Cfg) (*ParentInfo, error) {

	// parent.configのパスを取得する
	parentConfig := filepath.Join(cfg.TrafficServerConfigDir, ParentsFile)

	// parent.configの前回更新時刻を取得する
	modTime, err := util.GetFileModificationTime(parentConfig)
	if err != nil {
		return nil, errors.New("error reading " + ParentsFile + ": " + err.Error())
	}

	// parent.config用のConfigFile構造体にparent.configのパスと前回更新時刻を格納する
	parents := util.ConfigFile{
		Filename:       parentConfig,
		LastModifyTime: modTime,
	}

	// strategies.yamlのパスを取得する
	stratyaml := filepath.Join(cfg.TrafficServerConfigDir, StrategiesFile)

	// strategies.yamlの前回更新時刻を取得する
	modTime, err = util.GetFileModificationTime(stratyaml)
	if err != nil {
		return nil, errors.New("error reading " + StrategiesFile + ": " + err.Error())
	}

	// strategies.yaml用のConfigFile構造体にstrategies.yamlのパスと前回更新時刻を格納する
	strategies := util.ConfigFile{
		Filename:       filepath.Join(cfg.TrafficServerConfigDir, StrategiesFile),
		LastModifyTime: modTime,
	}

	// parent情報としてこの直前で作成したparent.configやstrategies.yamlのConfigFile構造体を指定する。また、trafficserverの情報も併せてしている
	parentInfo := ParentInfo{
		ParentDotConfig:        parents,
		StrategiesDotYaml:      strategies,
		TrafficServerBinDir:    cfg.TrafficServerBinDir,
		TrafficServerConfigDir: cfg.TrafficServerConfigDir,
	}

	// initialize the trafficserver parents map.
	parentStatus := make(map[string]ParentStatus)

	// read the 'parent.config'.
	// parent.configを読み込み、ParentStatus構造体を更新する
	if err := parentInfo.readParentConfig(parentStatus); err != nil {
		return nil, errors.New("loading " + ParentsFile + " file: " + err.Error())
	}

	// read the strategies.yaml.
	// strategies.yamlを読み込み、ParentStatus構造体を更新する
	if err := parentInfo.readStrategies(parentStatus); err != nil {
		return nil, errors.New("loading parent " + StrategiesFile + " file: " + err.Error())
	}

	// collect the trafficserver parent status from the HostStatus subsystem.
	// traffic_ctlコマンドによりhostのステータスを取得し、ParentStatus構造体を更新する
	if err := parentInfo.readHostStatus(parentStatus); err != nil {
		return nil, fmt.Errorf("reading trafficserver host status: %w", err)
	}

	log.Infof("startup loaded %d parent records\n", len(parentStatus))

	parentInfo.Parents = parentStatus
	parentInfo.Cfg = cfg

	return &parentInfo, nil
}

// Queries a traffic monitor that is monitoring the trafficserver instance running on a host to
// obtain the availability, health, of a parent used by trafficserver.
func (c *ParentInfo) GetCacheStatuses() (tc.CRStates, error) {

	// TrafficOpsから取得した複数台のTrafficMonitorから1台を決定する
	tmHostName, err := c.findATrafficMonitor()
	if err != nil {
		return tc.CRStates{}, errors.New("finding a trafficmonitor: " + err.Error())
	}

	// traffic_monitor/tmclient/tmclient.goが呼ばれる。初期値として「http://<monitorホスト名>」が指定される
	tmc := tmclient.New("http://"+tmHostName, config.GetRequestTimeout())

	// Use a proxy to query TM if the ProxyURL is set
	if c.Cfg.ParsedProxyURL != nil {
		tmc.Transport = &http.Transport{Proxy: http.ProxyURL(c.Cfg.ParsedProxyURL)}
	}

	// 「/publish/CrStates」にアクセスして取得した構造体の結果を応答する
	return tmc.CRStates(false)
}

// The main polling function that keeps the parents list current if
// with any changes to the trafficserver 'parent.config' or 'strategies.yaml'.
// Also, it keeps parent status current with the the trafficserver HostStatus
// subsystem.  Finally, on each poll cycle a trafficmonitor is queried to check
// that all parents used by this trafficserver are available for use based upon
// the trafficmonitors idea from it's health protocol.  Parents are marked up or
// down in the trafficserver subsystem based upon that hosts current status and
// the status that trafficmonitor health protocol has determined for a parent.
func (c *ParentInfo) PollAndUpdateCacheStatus() {

	toLoginDispersion := config.GetTOLoginDispersion(c.Cfg.TOLoginDispersionFactor)
	log.Infoln("polling started")
	log.Infof("TO login dispersion: %v seconds\n", toLoginDispersion.Seconds())

	// 無限ループ。tc-health-clientの主要な処理はこの中で実行される。
	for {

		pollingInterval := config.GetTMPollingInterval()

		// check for config file updates
		newCfg := config.Cfg{
			HealthClientConfigFile: c.Cfg.HealthClientConfigFile,
		}

		// 設定ファイルのmoddateが前回読み込み時刻よりも新しい場合には設定を読み込みし、isNewにはtrueが入ります。
		isNew, err := config.LoadConfig(&newCfg)
		if err != nil {
			log.Errorf("error reading changed config file %s: %s\n", c.Cfg.HealthClientConfigFile.Filename, err.Error())
		}

		if isNew {
			// 設定が読み込まれた場合

			// TrafficMonitor用のCredential情報を取得します
			if err = config.ReadCredentials(&newCfg, false); err != nil {
				log.Errorln("could not load credentials for config updates, keeping the old config")
			} else {

				if err = config.GetTrafficMonitors(&newCfg); err != nil {
					// TrafficMonitorのリスト取得に失敗した場合にはエラーメッセージを表示する
					log.Errorln("could not update the list of trafficmonitors, keeping the old config")
				} else {
					// TrafficMonitorのリスト取得に成功した場合

					// 既存の設定情報の更新を行う
					config.UpdateConfig(&c.Cfg, &newCfg)
					log.Infoln("the configuration has been successfully updated")
				}

			}

		} else { // check for updates to the credentials file
			// 設定が読み込まれない場合

			if c.Cfg.CredentialFile.Filename != "" {

				modTime, err := util.GetFileModificationTime(c.Cfg.CredentialFile.Filename)
				if err != nil {
					log.Errorf("could not stat the credential file %s", c.Cfg.CredentialFile.Filename)
				}

				if modTime > c.Cfg.CredentialFile.LastModifyTime {
					log.Infoln("the credential file has changed, loading new credentials")
					if err = config.ReadCredentials(&c.Cfg, true); err != nil {
						log.Errorf("could not load credentials from the updated credential file: %s", c.Cfg.CredentialFile.Filename)
					}
				}

			}
		}

		// check for parent and strategies config file updates, and trafficserver
		// host status changes.  If an error is encountered reading data the current
		// parents lists and hoststatus remains unchanged.
		// parent.config, strategies.yaml, traffic_ctlコマンドによるhost status変化などを確認してParentの構造体中の情報を更新する
		if err := c.UpdateParentInfo(); err != nil {
			log.Errorf("could not load new ATS parent info: %s\n", err.Error())
		} else {
			log.Debugf("updated parent info, total number of parents: %d\n", len(c.Parents))
		}

		// read traffic manager cache statuses.
		_c, err := c.GetCacheStatuses()

		// get the current poll time
		now := time.Now().Unix()

		caches := _c.Caches
		if err != nil {
			// キャッシュサーバの取得ができなかった場合

			log.Errorf("error in TrafficMonitor polling: %s\n", err.Error())

			// TrafficMonitorの情報を取得する
			if err = config.GetTrafficMonitors(&c.Cfg); err != nil {
				log.Errorln("could not update the list of trafficmonitors, keeping the old config")
			} else {
				log.Infoln("updated TrafficMonitor statuses from TrafficOps")
			}

			// log the poll state data if enabled
			if c.Cfg.EnablePollStateLog {
				err = c.WritePollState()
				if err != nil {
					log.Errorf("could not write the poll state log: %s\n", err.Error())
				}
			}

			time.Sleep(pollingInterval)
			continue
		}

		// 下記の$.cachesで処理をイテレーションしています。
		// see: https://traffic-control-cdn.readthedocs.io/en/latest/development/traffic_monitor/traffic_monitor_api.html#publish-crstates
		for k, v := range caches {
			hostName := string(k)
			cs, ok := c.Parents[hostName]
			if ok {

				// update the polling time
				cs.LastTmPoll = now
				c.Parents[hostName] = cs
				tmAvailable := v.IsAvailable

				if cs.available(c.Cfg.ReasonCode) != tmAvailable {

					// do not mark down if the configuration disables mark downs.
					if !c.Cfg.EnableActiveMarkdowns && !tmAvailable {
						log.Infof("TM reports that %s is not available and should be marked DOWN but, mark downs are disabled by configuration", hostName)
					} else {
						if err = c.markParent(cs.Fqdn, v.Status, tmAvailable); err != nil {
							log.Errorln(err.Error())
						}
					}

				}

				// if the host is available clear the unavailable poll count if not 0.
				if cs.available(c.Cfg.ReasonCode) && tmAvailable {
					if cs.UnavailablePollCount > 0 {
						log.Debugf("resetting the UnavailablePollCount for %s from %d to 0",
							hostName, cs.UnavailablePollCount)
						cs.UnavailablePollCount = 0
						c.Parents[hostName] = cs
					}
				}

			}
		}

		// periodically update the TrafficMonitor list and statuses
		// 定期的にTrafficMonitorのリストやステータスを更新する。
		if toLoginDispersion <= 0 {

			// toLoginDispersionが負の数になっていたら、更新処理を実施する。
			// toLoginDispersionの値はこの分岐のelseでポーリングするたびにポーリング間隔分の値が減かれている。

			// toLoginDispersionの値を再度決定する(ホスト名をハッシュ値に変換したりして決定する)
			toLoginDispersion = config.GetTOLoginDispersion(c.Cfg.TOLoginDispersionFactor)

			// TrafficOpsから最新のTrafficMonitorのステータス情報を取得して、次回からポーリング時に利用するTrafficMonitorのホスト名情報を１つ取得する
			if err = config.GetTrafficMonitors(&c.Cfg); err != nil {
				log.Errorln("could not update the list of trafficmonitors, keeping the old config")
			} else {
				log.Infoln("updated TrafficMonitor statuses from TrafficOps")
			}

		} else {
			// 算出した時間(toLoginDispersion)からpollingIntervalを差し引く
			toLoginDispersion -= pollingInterval
		}

		// log the poll state data if enabled
		// 設定ファイル中の「enable-poll-state-log」がtrueならば、実行される
		if c.Cfg.EnablePollStateLog {
			err = c.WritePollState()
			if err != nil {
				log.Errorf("could not write the poll state log: %s\n", err.Error())
			}
		}

		// 無限ループで実行されている次の処理まで、ここで指定された時間だけsleepする
		time.Sleep(pollingInterval)

	}
}

// Used by the polling function to update the parents list from
// changes to 'parent.config' and 'strategies.yaml'.  The parents
// availability is also updated to reflect the current state from
// the trafficserver HostStatus subsystem.
func (c *ParentInfo) UpdateParentInfo() error {

	// parent.configの前回更新時刻を取得する(※1)
	ptime, err := util.GetFileModificationTime(c.ParentDotConfig.Filename)
	if err != nil {
		return errors.New("error reading " + ParentsFile + ": " + err.Error())
	}

	// strategies.yamlの前回更新時刻を取得する(※2)
	stime, err := util.GetFileModificationTime(c.StrategiesDotYaml.Filename)
	if err != nil {
		return errors.New("error reading " + StrategiesFile + ": " + err.Error())
	}

	// 直前の(※1)で取得したparent.configの前回更新時刻が、保存しておいた前回取得時刻よりも大きければifのコードパスを実行する
	if c.ParentDotConfig.LastModifyTime < ptime {
		// read the 'parent.config'.
		// parent.configの情報をc.Parentsに書き込む
		if err := c.readParentConfig(c.Parents); err != nil {
			return errors.New("updating " + ParentsFile + " file: " + err.Error())
		} else {
			log.Infof("updated parents from new %s, total parents: %d\n", ParentsFile, len(c.Parents))
		}
	}

	// 直前の(※2)で取得したstrategies.yamlの前回更新時刻が、保存しておいた前回取得時刻よりも大きければifのコードパスを実行する
	if c.StrategiesDotYaml.LastModifyTime < stime {
		// read the 'strategies.yaml'.
		// strategies.yamlの情報をc.Parentsに書き込む
		if err := c.readStrategies(c.Parents); err != nil {
			return errors.New("updating parent " + StrategiesFile + " file: " + err.Error())
		} else {
			log.Infof("updated parents from new %s total parents: %d\n", StrategiesFile, len(c.Parents))
		}
	}

	// collect the trafficserver current host status.
	// trafficserverの現在のステータスをc.Parentsに書き込む
	if err := c.readHostStatus(c.Parents); err != nil {
		return errors.New("trafficserver may not be running: " + err.Error())
	}

	return nil
}

// 「/var/log/trafficcontrol/poll-state.json」にログ情報を書き込みます
func (c *ParentInfo) WritePollState() error {
	data, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return fmt.Errorf("marshaling configuration state: %s\n", err.Error())
	} else {
		// 「/var/log/trafficcontrol/poll-state.json」に書き込みます
		err = os.WriteFile(c.Cfg.PollStateJSONLog, data, 0644)
		if err != nil {
			return fmt.Errorf("writing configuration state: %s\n", err.Error())
		}
	}
	return nil
}

// choose an available trafficmonitor, returns an error if
// there are none.
// 複数台のTrafficMonitorから1台のTrafficMonitorを決定する
func (c *ParentInfo) findATrafficMonitor() (string, error) {

	var tmHostname string

	// tc-health-client/config/config.goのGetTrafficMonitors関数にてc.Cfg.TrafficMonitorsが登録される。
	lth := len(c.Cfg.TrafficMonitors)
	if lth == 0 {
		return "", errors.New("there are no available traffic monitors")
	}

	// build an array of available traffic monitors.
	tms := make([]string, 0)

	// tc-health-client/config/config.goのGetTrafficMonitors関数にて取得したtraffic_monitorのリストの値がtrueであれば、そのkeyであるk(TrafficMonitorのホスト名)を取得する
	for k, v := range c.Cfg.TrafficMonitors {
		if v == true {
			log.Debugf("traffic monitor %s is available\n", k)
			tms = append(tms, k)
		}
	}

	// choose one at random.
	// 複数台あるTrafficMonitorからランダム値によって1つのTrafficMonitorのみを決定します
	lth = len(tms)
	if lth > 0 {
		rand.Seed(time.Now().UnixNano())
		r := (rand.Intn(lth))
		tmHostname = tms[r]
	} else {
		return "", errors.New("there are no available traffic monitors")
	}

	log.Debugf("polling: %s\n", tmHostname)

	return tmHostname, nil
}

// parse out the hostname of a parent listed in parents.config
// or 'strategies.yaml'. the hostname can be an IP address.
func parseFqdn(fqdn string) string {
	var hostName string
	if ip := net.ParseIP(fqdn); ip == nil {
		// not an IP, get the hostname
		flds := strings.Split(fqdn, ".")
		hostName = flds[0]
	} else { // use the IP addr
		hostName = fqdn
	}
	return hostName
}

func (c *ParentInfo) execTrafficCtl(fqdn string, available bool) error {

	// TBD: reasonはどのようにして決めるのが良いのか?
	// see: https://docs.trafficserver.apache.org/en/latest/appendices/command-line/traffic_ctl.en.html#cmdoption-traffic_ctl-host-reason
	reason := c.Cfg.ReasonCode

	// traffic_ctlのパスを作成する
	tc := filepath.Join(c.TrafficServerBinDir, TrafficCtl)

	var status string
	if available {
		status = "up"
	} else {
		status = "down"
	}

	cmd := exec.Command(tc, "host", status, "--reason", reason, fqdn)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return errors.New("marking " + fqdn + " " + status + ": " + TrafficCtl + " error: " + err.Error())
	}

	return nil
}

// used to mark a parent as up or down in the trafficserver HostStatus
// subsystem.
func (c *ParentInfo) markParent(fqdn string, cacheStatus string, available bool) error {
	var hostAvailable bool
	var err error
	hostName := parseFqdn(fqdn)

	log.Debugf("fqdn: %s, available: %v", fqdn, available)

	pv, ok := c.Parents[hostName]
	if ok {

		activeReason := pv.ActiveReason
		localReason := pv.LocalReason
		unavailablePollCount := pv.UnavailablePollCount
		markUpPollCount := pv.MarkUpPollCount

		log.Debugf("hostName: %s, UnavailablePollCount: %d, available: %v", hostName, unavailablePollCount, available)

		// 「traffic_ctl host up 〜」や「traffic_ctl host down 〜」によりEDGE側のparent設定情報を変更することが可能である
		if !available { // unavailable
			unavailablePollCount += 1

			// 設定ファイル中のunavailable-poll-thresholdの設定の閾値によってそのままupさせるか、downさせるかを決定する
			if unavailablePollCount < c.Cfg.UnavailablePollThreshold {
				log.Infof("TM indicates %s is unavailable but the UnavailablePollThreshold has not been reached", hostName)
				hostAvailable = true
			} else {
				// marking the host down
				// 「例 traffic_ctl host down cdn-cache-01.foo.com --reason manual」 ここでは必ずdownが実行される
				err = c.execTrafficCtl(fqdn, available)
				if err != nil {
					log.Errorln(err.Error())
				} else {
					hostAvailable = false
					// reset the poll counts
					markUpPollCount = 0
					unavailablePollCount = 0
					log.Infof("marked parent %s DOWN, cache status was: %s\n", hostName, cacheStatus)
				}
			}

		} else { // available
			// marking the host up
			markUpPollCount += 1

			// 設定ファイル中のmarkup-poll-thresholdの設定の閾値によってそのままupさせるか、downさせるかを決定する
			if markUpPollCount < c.Cfg.MarkUpPollThreshold {
				log.Infof("TM indicates %s is available but the MarkUpPollThreshold has not been reached", hostName)
				hostAvailable = false
			} else {
				// 「例 traffic_ctl host up cdn-cache-01.foo.com --reason manual」 ここでは必ずupが実行される
				err = c.execTrafficCtl(fqdn, available)
				if err != nil {
					log.Errorln(err.Error())
				} else {
					hostAvailable = true
					// reset the poll counts
					unavailablePollCount = 0
					markUpPollCount = 0
					log.Infof("marked parent %s UP, cache status was: %s\n", hostName, cacheStatus)
				}
			}
		}

		// update parent info
		if err == nil {
			reason := c.Cfg.ReasonCode
			switch reason {
			case "active":
				activeReason = hostAvailable
			case "local":
				localReason = hostAvailable
			}
			// save updates
			pv.ActiveReason = activeReason
			pv.LocalReason = localReason
			pv.UnavailablePollCount = unavailablePollCount
			pv.MarkUpPollCount = markUpPollCount
			c.Parents[hostName] = pv
			log.Debugf("Updated parent status: %v", pv)
		}
	}
	return err
}

// reads the current parent statuses from the trafficserver HostStatus
// subsystem.
func (c *ParentInfo) readHostStatus(parentStatus map[string]ParentStatus) error {

	// traffic_ctlコマンドのパスを取得する
	tc := filepath.Join(c.TrafficServerBinDir, TrafficCtl)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	// auto select traffic_ctl command for ATS version 9 or 10 and later
	for i := traffic_ctl_index; i <= 1; i++ {

		var err error
		switch i {
		case 0: // ATS version 10 and later
			// 「$traffic_ctl host status」
			cmd := exec.Command(tc, "host", "status")
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
		case 1: // ATS version 9
			// 「$traffic_ctl metric match host_status」
			cmd := exec.Command(tc, "metric", "match", "host_status")
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
		}

		// traffic_ctlコマンドを実行してエラーでなければ、そのまま処理をbreakする
		if err == nil {
			break
		}

		// 最初のindex値(i=0)でtraffic_ctlコマンドを実行してエラーだった場合には、traffic_ctl_index=1 (ATS9)として実行する
		if err != nil && i == 0 {
			log.Infof("%s command used is not for ATS version 10 or later, downgrading to ATS version 9\n", TrafficCtl)
			traffic_ctl_index = 1
			continue
		}

		// i=1(ATS9)で、traffic_ctlコマンドがエラーな場合には、エラーとする
		if err != nil {
			return fmt.Errorf("%s error: %s", TrafficCtl, stderr.String())
		}

	}

	// traffic_ctlコマンドの出力結果があれば、if文のコードパスが実行される
	if len((stdout.Bytes())) > 0 {

		var activeReason bool
		var localReason bool
		var manualReason bool
		var hostName string
		var fqdn string

		scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
		for scanner.Scan() {

			// 行を取得してtrimし、スペースでセパレートしてfieldsに格納する
			line := strings.TrimSpace(scanner.Text())
			fields := strings.Split(line, " ")

			/*
			 * For ATS Version 9, the host status uses internal stats and prefixes
			 * the fqdn field from the output of the traffic_ctl host status and metric
			 * match commands with "proxy.process.host_status".  Going forward starting
			 * with ATS Version 10, internal stats are no-longer used and the fqdn field
			 * is no-longer prefixed with the "proxy.process.host_status" string.
			 */
			if len(fields) == 2 {

				// check for ATS version 9 output.
				fqdnField := strings.Split(fields[0], "proxy.process.host_status.")
				if len(fqdnField) == 2 { // ATS version 9
					fqdn = fqdnField[1]
				} else { // ATS version 10 and greater
					fqdn = fqdnField[0]
				}

				// $ traffic_ctl metric match host_status の出力サンプル
				//   proxy.process.host_status.cdn-cache-01.foo.com HOST_STATUS_DOWN,ACTIVE:UP:0:0,LOCAL:UP:0:0,MANUAL:DOWN:1556896844:0,SELF_DETECT:UP:0
				//   proxy.process.host_status.cdn-cache-02.foo.com HOST_STATUS_UP,ACTIVE:UP:0:0,LOCAL:UP:0:0,MANUAL:UP:0:0,SELF_DETECT:UP:0
				//   proxy.process.host_status.cdn-cache-origin-01.foo.com HOST_STATUS_UP,ACTIVE:UP:0:0,LOCAL:UP:0:0,MANUAL:UP:0:0,SELF_DETECT:UP:0
				// 
				// cf. https://docs.trafficserver.apache.org/en/latest/appendices/command-line/traffic_ctl.en.html#cmdoption-traffic_ctl-host-reason

				// 「ACTIVE:UP:0:0,LOCAL:UP:0:0,MANUAL:DOWN:1556896844:0,SELF_DETECT:UP:0」の部分がfields[1]となる。それを「,」でセパレートする
				statField := strings.Split(fields[1], ",")
				if len(statField) == 5 {

					// activeReasonの決定
					if strings.HasPrefix(statField[1], "ACTIVE:UP") {
						activeReason = true
					} else if strings.HasPrefix(statField[1], "ACTIVE:DOWN") {
						activeReason = false
					}

					// localReasenの決定
					if strings.HasPrefix(statField[2], "LOCAL:UP") {
						localReason = true
					} else if strings.HasPrefix(statField[2], "LOCAL:DOWN") {
						localReason = false
					}

					// manualReasonの決定
					if strings.HasPrefix(statField[3], "MANUAL:UP") {
						manualReason = true
					} else if strings.HasPrefix(statField[3], "MANUAL:DOWN") {
						manualReason = false
					}

				}

				// ParentStatus構造体に上で決定した各種xxxxReasonの値をセットする
				pstat := ParentStatus{
					Fqdn:                 fqdn,
					ActiveReason:         activeReason,
					LocalReason:          localReason,
					ManualReason:         manualReason,
					LastTmPoll:           0,
					UnavailablePollCount: 0,
					MarkUpPollCount:      0,
				}

				// parentStatusを上書きする
				log.Debugf("processed host status record: %v\n", pstat)
				hostName = parseFqdn(fqdn)
				pv, ok := parentStatus[hostName]
				// create the ParentStatus struct and add it to the
				// Parents map only if an entry in the map does not
				// already exist.
				if !ok {
					parentStatus[hostName] = pstat
					log.Infof("added Host '%s' from ATS Host Status to the parents map\n", hostName)
				} else {
					available := pstat.available(c.Cfg.ReasonCode)
					if pv.available(c.Cfg.ReasonCode) != available {
						log.Infof("host status for '%s' has changed to %s\n", hostName, pstat.Status())
						pstat.LastTmPoll = pv.LastTmPoll
						pstat.UnavailablePollCount = pv.UnavailablePollCount
						pstat.MarkUpPollCount = pv.MarkUpPollCount
						parentStatus[hostName] = pstat
					}
				}
			}

		}

		log.Debugf("processed trafficserver host status results, total parents: %d\n", len(parentStatus))

	}

	return nil

}

// load parents list from the Trafficserver 'parent.config' file.
func (c *ParentInfo) readParentConfig(parentStatus map[string]ParentStatus) error {
	fn := c.ParentDotConfig.Filename

	_, err := os.Stat(fn)
	if err != nil {
		log.Warnf("skipping 'parents': %s\n", err.Error())
		return nil
	}

	log.Debugf("loading %s\n", fn)

	f, err := os.Open(fn)

	if err != nil {
		return errors.New("failed to open + " + fn + " :" + err.Error())
	}
	defer f.Close()

	finfo, err := os.Stat(fn)
	if err != nil {
		return errors.New("failed to Stat + " + fn + " :" + err.Error())
	}

	// parent.configの前回更新時刻を取得する
	c.ParentDotConfig.LastModifyTime = finfo.ModTime().UnixNano()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {

		sbytes := scanner.Bytes()
		if sbytes[0] == 35 { // skip comment lines, 35 is a '#'.
			continue
		}

		// search for the parent list.
		if i := strings.Index(string(sbytes), "parent="); i > 0 {
			var plist []string
			res := bytes.Split(sbytes, []byte("\""))
			// 'parent.config' parent separators are ';' or ','.
			plist = strings.Split(strings.TrimSpace(string(res[1])), ";")
			if len(plist) == 1 {
				plist = strings.Split(strings.TrimSpace(string(res[1])), ",")
			}

			// parse the parent list to get each hostName and it's associated
			// port.
			if len(plist) > 1 {
				for _, v := range plist {
					parent := strings.Split(v, ":")
					if len(parent) == 2 {
						fqdn := parent[0]
						hostName := parseFqdn(fqdn)
						_, ok := parentStatus[hostName]
						// create the ParentStatus struct and add it to the
						// Parents map only if an entry in the map does not
						// already exist.
						if !ok {
							pstat := ParentStatus{
								Fqdn:                 strings.TrimSpace(fqdn),
								ActiveReason:         true,
								LocalReason:          true,
								ManualReason:         true,
								LastTmPoll:           0,
								UnavailablePollCount: 0,
							}
							parentStatus[hostName] = pstat
							log.Debugf("added Host '%s' from %s to the parents map\n", hostName, fn)
						}
					}
				}
			}
		}
	}
	return nil
}

// load the parent hosts from 'strategies.yaml'.
// strategies.yamlを読み込み、ParentStatus構造体に必要な情報をセットする
func (c *ParentInfo) readStrategies(parentStatus map[string]ParentStatus) error {
	var includes []string
	fn := c.StrategiesDotYaml.Filename

	_, err := os.Stat(fn)
	if err != nil {
		log.Warnf("skipping 'strategies': %s\n", err.Error())
		return nil
	}

	log.Debugf("loading %s\n", fn)

	// open the strategies file for scanning.
	f, err := os.Open(fn)
	if err != nil {
		return errors.New("failed to open + " + fn + " :" + err.Error())
	}
	defer f.Close()

	finfo, err := os.Stat(fn)
	if err != nil {
		return errors.New("failed to Stat + " + fn + " :" + err.Error())
	}
	c.StrategiesDotYaml.LastModifyTime = finfo.ModTime().UnixNano()

	scanner := bufio.NewScanner(f)

	// search for any yaml files that should be included in the
	// yaml stream.
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#include") {
			fields := strings.Split(line, " ")
			if len(fields) >= 2 {
				includeFile := filepath.Join(c.TrafficServerConfigDir, fields[1])
				includes = append(includes, includeFile)
			}
		}
	}

	includes = append(includes, fn)

	var yamlContent string

	// load all included and 'strategies yaml' files to
	// the yamlContent.
	for _, includeFile := range includes {
		log.Debugf("loading %s\n", includeFile)
		content, err := ioutil.ReadFile(includeFile)
		if err != nil {
			return errors.New(err.Error())
		}

		yamlContent = yamlContent + string(content)
	}

	strategies := Strategies{}

	if err := yaml.Unmarshal([]byte(yamlContent), &strategies); err != nil {
		return errors.New("failed to unmarshall " + fn + ": " + err.Error())
	}

	for _, host := range strategies.Hosts {
		fqdn := host.HostName
		hostName := parseFqdn(fqdn)
		// create the ParentStatus struct and add it to the
		// Parents map only if an entry in the map does not
		// already exist.
		_, ok := parentStatus[hostName]
		if !ok {
			pstat := ParentStatus{
				Fqdn:                 strings.TrimSpace(fqdn),
				ActiveReason:         true,
				LocalReason:          true,
				ManualReason:         true,
				LastTmPoll:           0,
				UnavailablePollCount: 0,
			}
			parentStatus[hostName] = pstat
			log.Debugf("added Host '%s' from %s to the parents map\n", hostName, fn)
		}
	}
	return nil
}
