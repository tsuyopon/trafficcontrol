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


// ファイル文字列や正規表現によるファイルパターンに対して、ファイルの内容の書き換え編集操作を行います。
// see: https://www.npmjs.com/package/grunt-string-replace
module.exports = {

		files: {

			// expand=trueを指定することで、下記のcwd, dest, srcなどのオプション群が指定できるようになる。
			//   cf. https://gruntjs.com/configuring-tasks#building-the-files-object-dynamically
			expand: true,

			// cwdとdestが同じであることに注意(つまり、入力と出力が同じと思われる)
			cwd: '<%= globalConfig.distdir %>/public',

			// 出力先パス
			dest: '<%= globalConfig.distdir %>/public',

			// index.html
			src: 'index.html',
		},

		// 書き換えるファイルパターンとその置換後の文字列
		// 主にlink relやscriptタグなどで「?built=<Date.now()>」のクエリストリングを付与する目的で利用される。
		options: {
			replacements: [
				{
					pattern: 'theme.css',
					replacement: 'theme.css?built=' + Date.now()
				},
				{
					pattern: 'loading.css',
					replacement: 'loading.css?built=' + Date.now()
				},
				{
					pattern: 'main.css',
					replacement: 'main.css?built=' + Date.now()
				},
				{
					pattern: 'custom.css',
					replacement: 'custom.css?built=' + Date.now()
				},
				{
					pattern: 'shared-libs.js',
					replacement: 'shared-libs.js?built=' + Date.now()
				},
				{
					pattern: 'app.js',
					replacement: 'app.js?built=' + Date.now()
				},
				{
					pattern: 'config.js',
					replacement: 'config.js?built=' + Date.now()
				}
			]
		}
};
