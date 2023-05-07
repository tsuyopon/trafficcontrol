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

'use strict';

module.exports = function (grunt) {

    var os = require("os");

    // grunt/globalConfig.js を読み込みます。これが設定ファイルとして利用されています
    var globalConfig = require('./grunt/globalConfig');

    // load time grunt - helps with optimizing build times
    require('time-grunt')(grunt);

    // load grunt task configurations
    require('load-grunt-config')(grunt);

    // load string replacement functionality. used to add query params to index.html to bust cache on upgrade
    require('grunt-string-replace')(grunt);

    // default task - when you type 'grunt' it really runs as 'grunt dev'
    grunt.registerTask('default', ['dev']);


    /*
     * (重要) grunt.registerTaskの第2引数に記載している「build-dev」「express」「watch」などは以下の1, 2のいずれかに一致する。
     *  - 1. grunt.registerTaskとして登録されているもの(build-devなどは別の箇所で定義されている)
     *  - 2.「express:dev」「watch」などは1のように定義されていないので、この場合にはgruntディレクトリ配下に.jsのsuffixを付与した同名のファイルとして存在する。つまり、grunt/express.jsやgrunt/watch.jsに存在する。
     *
     */
    // dev task - when you type 'grunt dev' <-- builds unminified app and puts it in in app/dist folder and starts express server which reads server.js
    grunt.registerTask('dev', [
        'build-dev',
        'express:dev',
        'watch'
    ]);

    // dist task - when you type 'grunt dist' <-- builds minified app for distribution and generates node dependencies all wrapped up nicely in app/dist folder
    // Dockerfileのbuildの中で「grunt dist」が呼ばれる。つまり、これがgruntに関する起点となるメイン処理と思われる
    grunt.registerTask('dist', [
        'build',
        'install-dependencies'
    ]);

    // build tasks
    // 「grunt dist」からこのbuildが呼ばれる
    grunt.registerTask('build', [
        'clean',
        'copy:dist',
        'string-replace',
        'build-css',
        'build-js',
        'build-shared-libs'
    ]);

    grunt.registerTask('build-dev', [
        'clean',
        'copy:dev',
        'string-replace',
        'build-css-dev',
        'build-js-dev',
        'build-shared-libs-dev'
    ]);

    // css
    // 「./grunt/dart-sass.js」ファイルがあり、その中にprodやdevとあるのでそれらに適用されるものと思われる
    grunt.registerTask('build-css', [
        'dart-sass:prod'
    ]);

    grunt.registerTask('build-css-dev', [
        'dart-sass:dev'
    ]);

    // js (custom)
    grunt.registerTask('build-js', [
        'html2js',
        'browserify:app-prod',
        'browserify:app-config'
    ]);

    grunt.registerTask('build-js-dev', [
        'html2js',
        'browserify:app-dev',
        'browserify:app-config'
    ]);

    // js (libraries)
    // 「grunt/browserify.js」ファイルにshared-libs-prodについて記載があるので、それに適用されるものと思われる
    grunt.registerTask('build-shared-libs', [
        'browserify:shared-libs-prod'
    ]);

    grunt.registerTask('build-shared-libs-dev', [
        'browserify:shared-libs-dev'
    ]);

};
