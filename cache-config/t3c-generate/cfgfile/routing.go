package cfgfile

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
	"strings"
	"time"

	"github.com/apache/trafficcontrol/cache-config/t3c-generate/config"
	"github.com/apache/trafficcontrol/cache-config/t3cutil"
	"github.com/apache/trafficcontrol/lib/go-atscfg"
	"github.com/apache/trafficcontrol/lib/go-log"
)

// # DO NOT EDIT - Generated for odol-atsec-sea-22 by Traffic Ops (https://trafficops.comcast.net/) on Mon Oct 26 16:22:19 UTC 2020

// GetConfigFile returns the text of the generated config file, the MIME Content Type of the config file, and any error.
func GetConfigFile(toData *t3cutil.ConfigData, fileInfo atscfg.CfgMeta, hdrCommentTxt string, thiscfg config.Cfg) (string, string, bool, string, []string, error) {

	start := time.Now()
	defer func() {
		log.Infof("GetConfigFile %v took %v\n", fileInfo.Name, time.Since(start).Round(time.Millisecond))
	}()
	log.Infoln("GetConfigFile '" + fileInfo.Name + "'")

	// ファイル名に対応したハンドラ処理があれば、その関数ハンドラがreturnされる。指定したファイル名にハンドラが一致しない場合には、一致しなかった場合の専用の反dらが応答される
	getConfigFile := getConfigFileFunc(fileInfo.Name)
	cfg, err := getConfigFile(toData, fileInfo.Name, hdrCommentTxt, thiscfg)
	logWarnings("getting config file '"+fileInfo.Name+"': ", cfg.Warnings)

	if err != nil {
		return "", "", false, "", []string{}, err
	}

	return cfg.Text, cfg.ContentType, cfg.Secure, cfg.LineComment, cfg.Warnings, nil
}

type ConfigFileFunc func(toData *t3cutil.ConfigData, fileName string, hdrCommentTxt string, cfg config.Cfg) (atscfg.Cfg, error)

type ConfigFilePrefixSuffixFunc struct {
	Prefix string
	Suffix string
	Func   ConfigFileFunc
}

type ConfigFileLiteralFunc struct {
	Name string
	Func ConfigFileFunc
}

func getConfigFileFunc(fileName string) ConfigFileFunc {

	// 指定されたファイルが特定のファイル名に一致した際に実行すべき関数ハンドラへの応答をreturnする
	for _, lf := range configFileLiteralFuncs {
		// ファイル名が完全マッチした場合
		if fileName == lf.Name {
			return lf.Func
		}
	}

	// ファイル名のprefixとsuffixが共にマッチした場合に実行すべき関数ハンドラへの応答をreturnする
	for _, psf := range configFilePrefixSuffixFuncs {
		// ファイル名のprefixとsuffixが共にマッチした場合
		if strings.HasPrefix(fileName, psf.Prefix) && strings.HasSuffix(fileName, psf.Suffix) {
			return psf.Func
		}
	}

	return MakeUnknownConfig
}

// ファイル名に対するハンドラへのマッピングを定義している
// see: https://traffic-control-cdn.readthedocs.io/en/latest/overview/profiles_and_parameters.html
var configFileLiteralFuncs = []ConfigFileLiteralFunc{
	{"12M_facts", Make12MFacts},
	{"50-ats.rules", MakeATSDotRules},
	{"astats.config", MakeAstatsDotConfig},
	{"bg_fetch.config", MakeBGFetchDotConfig},
	{"cache.config", MakeCacheDotConfig},
	{"chkconfig", MakeChkconfig},
	{"drop_qstring.config", MakeDropQStringDotConfig},
	{"hosting.config", MakeHostingDotConfig},
	{"ip_allow.config", MakeIPAllowDotConfig},
	{"ip_allow.yaml", MakeIPAllowDotYAML},
	{"logging.config", MakeLoggingDotConfig},
	{"logging.yaml", MakeLoggingDotYAML},
	{"logs_xml.config", MakeLogsXMLDotConfig},
	{"packages", MakePackages},
	{"parent.config", MakeParentDotConfig},
	{"plugin.config", MakePluginDotConfig},
	{"records.config", MakeRecordsDotConfig},
	{"regex_revalidate.config", MakeRegexRevalidateDotConfig},
	{"remap.config", MakeRemapDotConfig},
	{"ssl_multicert.config", MakeSSLMultiCertDotConfig},
	{"ssl_server_name.yaml", MakeSSLServerNameYAML},
	{"sni.yaml", MakeSNIDotYAML},
	{"strategies.yaml", MakeStrategiesDotYAML},
	{"storage.config", MakeStorageDotConfig},
	{"sysctl.conf", MakeSysCtlDotConf},
	{"volume.config", MakeVolumeDotConfig},
}

// ファイル名のprefixとsuffixに基づき、それらのファイルに対してどのような処理を施すかのマッピングを定義する
var configFilePrefixSuffixFuncs = []ConfigFilePrefixSuffixFunc{
	{atscfg.HeaderRewriteFirstPrefix, ".config", MakeHeaderRewrite},
	{atscfg.HeaderRewriteInnerPrefix, ".config", MakeHeaderRewrite},
	{atscfg.HeaderRewriteLastPrefix, ".config", MakeHeaderRewrite},
	{"hdr_rw_mid_", ".config", MakeHeaderRewrite},
	{"hdr_rw_", ".config", MakeHeaderRewrite},
	{"regex_remap_", ".config", MakeRegexRemap},
	{"set_dscp_", ".config", MakeSetDSCP},
	{"url_sig_", ".config", MakeURLSigConfig},
	{"uri_signing_", ".config", MakeURISigningConfig},
}
