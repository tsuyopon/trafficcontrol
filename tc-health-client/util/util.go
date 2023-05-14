package util

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
	"os"
)

// 設定に関する情報を格納する構造体
type ConfigFile struct {
	// ファイル名
	Filename       string

	// 前回更新時刻が保存される
	LastModifyTime int64
}

// get the file modification times for a configuration file.
// ファイルの前回更新時刻を取得する
func GetFileModificationTime(fn string) (int64, error) {

	f, err := os.Open(fn)
	if err != nil {
		return 0, errors.New("opening " + fn + ": " + err.Error())
	}
	defer f.Close()

	// fileのstatを取得する
	finfo, err := f.Stat()
	if err != nil {
		return 0, errors.New("unable to get file status for " + fn + ": " + err.Error())
	}

	// 前回の変更時刻を取得する
	return finfo.ModTime().UnixNano(), nil

}
