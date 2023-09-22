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


// ファイルやディレクトリをコピーする
// see: https://www.npmjs.com/package/grunt-contrib-copy

module.exports = {
    dev: {
        files: [
            {
                // expand=trueを指定することで、下記のcwd, dest, srcなどのオプション群が指定できるようになる。
                //   cf. https://gruntjs.com/configuring-tasks#building-the-files-object-dynamically
                expand: true,

                // パターンが明示的にその場所にピリオドを持たない場合でも、ピリオドで始まるファイル名とマッチするようにする。 (TODO: 意味不明。要確認)
                // cf. https://gruntjs.com/configuring-tasks#files
                dot: true,

                // 現在のディレクトリ(app/src)
                cwd: '<%= globalConfig.srcdir %>',

                // コピー先の指定(app/dist/public/resources)
                dest: '<%= globalConfig.resourcesdir %>',

                // コピー元ファイルの指定 (cwdからの相対パスが指定される)
                src: [
                    'assets/css/**/*',
                    'assets/fonts/**/*',
                    'assets/images/**/*',
                    'assets/js/**/*'
                ]
            },
            {
                expand: true,
                dot: true,
                cwd: '<%= globalConfig.srcdir %>',
                dest: '<%= globalConfig.distdir %>/public',
                src: [
                    '*.html',
                    'traffic_portal_release.json',
                    'traffic_portal_properties.json'
                ]
            },
            {
                expand: true,
                dot: true,
                cwd: "<%= globalConfig.importdir %>",
                dest: "<%= globalConfig.resourcesdir %>/assets/js/",
                src: ["ag-grid-community/dist/ag-grid-community.min.js"]
            }
        ]
    },
    dist: {
        files: [
            {
                expand: true,
                dot: true,
                cwd: '<%= globalConfig.srcdir %>',
                dest: '<%= globalConfig.resourcesdir %>',
                src: [
                    'assets/css/**/*',
                    'assets/fonts/**/*',
                    'assets/images/**/*',
                    'assets/js/**/*'
                ]
            },
            // 下記のpackage.jsonの定義が「dev」の定義と比べて「dist」は多い
            {
                expand: true,
                dot: true,
                cwd: '<%= globalConfig.srcdir %>',
                dest: '<%= globalConfig.distdir %>',
                src: [
                    'package.json'
                ]
            },
            {
                expand: true,
                dot: true,
                cwd: '<%= globalConfig.srcdir %>',
                dest: '<%= globalConfig.distdir %>/public',
                src: [
                    '*.html',
                    'traffic_portal_release.json',
                    'traffic_portal_properties.json'
                ]
            },
            {
                expand: true,
                dot: true,
                cwd: "<%= globalConfig.importdir %>",
                dest: "<%= globalConfig.resourcesdir %>/assets/js/",
                src: ["ag-grid-community/dist/ag-grid-community.min.js"]
            }
        ]
    }
};
