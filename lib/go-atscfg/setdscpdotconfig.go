package atscfg

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
	"strconv"
	"strings"
)

const ContentTypeSetDSCPDotConfig = ContentTypeTextASCII
const LineCommentSetDSCPDotConfig = LineCommentHash

// SetDSCPDotConfigOpts contains settings to configure generation options.
type SetDSCPDotConfigOpts struct {
	// HdrComment is the header comment to include at the beginning of the file.
	// This should be the text desired, without comment syntax (like # or //). The file's comment syntax will be added.
	// To omit the header comment, pass the empty string.
	HdrComment string
}

func MakeSetDSCPDotConfig(

	fileName string,
	server *Server,
	opt *SetDSCPDotConfigOpts,
) (Cfg, error) {
	if opt == nil {
		opt = &SetDSCPDotConfigOpts{}
	}
	warnings := []string{}

	// CDNNameが存在しなければエラーでreturnする
	if server.CDNName == nil {
		return Cfg{}, makeErr(warnings, "server missing CDNName")
	}

	// TODO verify prefix, suffix, and that it's a number? Perl doesn't.
	dscpNumStr := fileName
	dscpNumStr = strings.TrimPrefix(dscpNumStr, "set_dscp_")
	dscpNumStr = strings.TrimSuffix(dscpNumStr, ".config")

	text := makeHdrComment(opt.HdrComment)

	// ファイル名のprefixから「set_dscp_」を除去、suffixから「.config」を除去した文字列が数字であるかどうかを確認する。
	if _, err := strconv.Atoi(dscpNumStr); err != nil {
		// TODO warn? We don't generally warn for client errors, because it can be an attack vector. Provide a more informative error return? Return a 404?
		text = "An error occured generating the DSCP header rewrite file."
		dscpNumStr = "" // emulates Perl
	}

	// 「set_dscp_xxx.config」とconfigFileに設定されている場合には下記の内容がそのファイルのコンテンツ情報となります。
	text += `cond %{REMAP_PSEUDO_HOOK}` + "\n" + `set-conn-dscp ` + dscpNumStr + ` [L]` + "\n"

	return Cfg{
		Text:        text,
		ContentType: ContentTypeSetDSCPDotConfig,
		LineComment: LineCommentSetDSCPDotConfig,
		Warnings:    warnings,
	}, nil
}
