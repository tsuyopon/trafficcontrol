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


// grunt-contrib-watchはファイルの変更を検知したら、ブラウザで読み直す仕組みのようです
//  cf. https://github.com/gruntjs/grunt-contrib-watch
//
// その他参考
//   https://ledsun.hatenablog.com/entry/2013/11/28/181035
module.exports = {

    options: {
        // livereloadを有効にする。デフォルトではポート番号は35729
        livereload: true
    },

    // filesで指定された.jsファイルの変更が検知されたらbuild-devタスクを実行する
    js: {
        files: ['<%= globalConfig.srcdir %>/**/*.js'],
        tasks: [ 'build-dev']
    },

    // filesで指定された.scss, .sassファイルの変更が検知されたらdart-sass:devタスクを実行する
    // 調べてみたが下記の「compass」というのは特別な意味はないものと思われる。設定を識別するための値と考えておくとよさそう
    compass: {
        files: ['<%= globalConfig.srcdir %>/**/*.{scss,sass}'],
        tasks: ['dart-sass:dev']
    },

    // filesで指定されたhtmlファイルの変更が検知されたらcopy:distを実行し、build-devタスクを実行する
    html: {
        files: ['app/**/*.tpl.html', 'app/**/index.html'],
        tasks: ['copy:dist', 'build-dev']
    }
};
