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

// browserify.jsはnode.jsで解釈可能なJavaScript記法で記述することができます。それに対して変換をかけることによって、ブラウザでも解釈できるように変換してくれます。
// 参考: https://www.codegrid.net/articles/2015-browserify-1/
//
module.exports = {

    // 指定した共通系ライブラリをまとめる。prod用途
    // srcで読み込み、requireで指定された箇所をoptions.aliasで指定された各種js(主に.min.js)にて保管して、destに書き出す。
    'shared-libs-prod': {
        src: ['./<%= globalConfig.srcdir %>/scripts/shared-libs.js'],
        dest: './<%= globalConfig.resourcesdir %>/assets/js/shared-libs.js',
        options: {
            alias: {
                "angular": "./<%= globalConfig.importdir %>/angular/angular.min.js",
                "angular-animate": './<%= globalConfig.importdir %>/angular-animate/angular-animate.min.js',
                "contextMenu": './<%= globalConfig.importdir %>/angular-bootstrap-contextmenu/contextMenu.js',
                "ui-bootstrap": './<%= globalConfig.importdir %>/angular-ui-bootstrap/ui-bootstrap.min.js',
                "ui-bootstrap-tpls": './<%= globalConfig.importdir %>/angular-ui-bootstrap/ui-bootstrap-tpls.min.js',
                "angular-jwt": './<%= globalConfig.importdir %>/angular-jwt/dist/angular-jwt.min.js',
                "loading-bar": './<%= globalConfig.importdir %>/angular-loading-bar/build/loading-bar.min.js',
                "angular-resource": './<%= globalConfig.importdir %>/angular-resource/angular-resource.min.js',
                "angular-route": './<%= globalConfig.importdir %>/angular-route/angular-route.min.js',
                "angular-sanitize": './<%= globalConfig.importdir %>/angular-sanitize/angular-sanitize.min.js',
                "angular-ui-router": './<%= globalConfig.importdir %>/@uirouter/angularjs/release/angular-ui-router.min.js',
                "bootstrap": './<%= globalConfig.importdir %>/bootstrap-sass/assets/javascripts/bootstrap.min.js',
                "es5-shim": './<%= globalConfig.importdir %>/es5-shim/es5-shim.min.js',
                "jquery": './<%= globalConfig.importdir %>/jquery/dist/jquery.min.js',
                "json3": './<%= globalConfig.importdir %>/json3/lib/json3.min.js',
                'jquery-flot': './<%= globalConfig.importdir %>/flot/dist/es5/jquery.flot.js',
                'jquery-flot-pie': './<%= globalConfig.importdir %>/flot/source/jquery.flot.pie.js',
                'jquery-flot-stack': './<%= globalConfig.importdir %>/flot/source/jquery.flot.stack.js',
                'jquery-flot-time': './<%= globalConfig.importdir %>/flot/source/jquery.flot.time.js',
                'jquery-flot-tooltip': './<%= globalConfig.importdir %>/jquery.flot.tooltip/js/jquery.flot.tooltip.js',
                'jquery-flot-axislabels': './<%= globalConfig.importdir %>/flot/source/jquery.flot.axislabels.js',
            },
        },
    },

    // 指定した共通系ライブラリをまとめる。dev用途
    // srcで読み込み、requireで指定された箇所をoptions.aliasで指定された各種js(主に.min.js)にて保管して、destに書き出す。
    'shared-libs-dev': {
        src: ['./<%= globalConfig.srcdir %>/scripts/shared-libs.js'],
        dest: './<%= globalConfig.resourcesdir %>/assets/js/shared-libs.js',
        options: {
            alias: {
                "angular": "./<%= globalConfig.importdir %>/angular/angular.min.js",
                "angular-animate": './<%= globalConfig.importdir %>/angular-animate/angular-animate.min.js',
                "contextMenu": './<%= globalConfig.importdir %>/angular-bootstrap-contextmenu/contextMenu.js',
                "ui-bootstrap": './<%= globalConfig.importdir %>/angular-ui-bootstrap/ui-bootstrap.min.js',
                "ui-bootstrap-tpls": './<%= globalConfig.importdir %>/angular-ui-bootstrap/ui-bootstrap-tpls.min.js',
                "angular-jwt": './<%= globalConfig.importdir %>/angular-jwt/dist/angular-jwt.min.js',
                "loading-bar": './<%= globalConfig.importdir %>/angular-loading-bar/build/loading-bar.min.js',
                "angular-resource": './<%= globalConfig.importdir %>/angular-resource/angular-resource.min.js',
                "angular-route": './<%= globalConfig.importdir %>/angular-route/angular-route.min.js',
                "angular-sanitize": './<%= globalConfig.importdir %>/angular-sanitize/angular-sanitize.min.js',
                "angular-ui-router": './<%= globalConfig.importdir %>/@uirouter/angularjs/release/angular-ui-router.min.js',
                "bootstrap": './<%= globalConfig.importdir %>/bootstrap-sass/assets/javascripts/bootstrap.min.js',
                "es5-shim": './<%= globalConfig.importdir %>/es5-shim/es5-shim.min.js',
                "jquery": './<%= globalConfig.importdir %>/jquery/dist/jquery.js',
                "json3": './<%= globalConfig.importdir %>/json3/lib/json3.min.js',
                'jquery-flot': './<%= globalConfig.importdir %>/flot/dist/es5/jquery.flot.js',
                'jquery-flot-pie': './<%= globalConfig.importdir %>/flot/source/jquery.flot.pie.js',
                'jquery-flot-stack': './<%= globalConfig.importdir %>/flot/source/jquery.flot.stack.js',
                'jquery-flot-time': './<%= globalConfig.importdir %>/flot/source/jquery.flot.time.js',
                'jquery-flot-tooltip': './<%= globalConfig.importdir %>/jquery.flot.tooltip/js/jquery.flot.tooltip.js',
                'jquery-flot-axislabels': './<%= globalConfig.importdir %>/flot/source/jquery.flot.axislabels.js',
            },
        },
    },

    // アプリケーションに特化したライブラリapp.jsを生成する(コンパイルされると50000行以上のjsコードになります)
    // このapp.jsの中からconfig.jsにアクセスしたり、traffic_portal_properties.jsonといった設定ファイルにアクセスするコードや、TrafficOps WebAPIにアクセスする共通コードが含まれています。
    //
    // options.aliasに指定されているapp-template.jsはgrunt/html2js.jsによって生成されるようです。
    'app-prod': {
        src: ['./<%= globalConfig.srcdir %>/app.js'],
        dest: './<%= globalConfig.resourcesdir %>/assets/js/app.js',
        browserifyOptions: {
            debug: false,
        },
        options: {
            alias: {
                'app-templates': './<%= globalConfig.tmpdir %>/app-templates.js'
            }
        }
    },

    // app-prodのdebug用のようです。browserifyOptions=true
    'app-dev': {
        src: ['./<%= globalConfig.srcdir %>/app.js'],
        dest: './<%= globalConfig.resourcesdir %>/assets/js/app.js',
        browserifyOptions: {
            // TODO: どのようなデバッグできるのかは詳細不明: https://github.com/jmreidy/grunt-browserify/blob/fd0f242873afc3d8bd4ed80df8bd462f52b72dea/README.md#browserifyoptions
            debug: true,
        },
        options: {
            alias: {
                'app-templates':'./<%= globalConfig.tmpdir %>/app-templates.js'
            }
        }
    },
    'app-config': {
        src: ['./<%= globalConfig.srcdir %>/scripts/config.js'],
        dest: './<%= globalConfig.resourcesdir %>/assets/js/config.js'
    }
};
