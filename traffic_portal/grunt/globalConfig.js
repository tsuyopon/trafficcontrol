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

// このファイルは Gruntfile.js から下記のようにして読み込まれ、さまざまな共通設定として利用されることになります。
//    var globalConfig = require('./grunt/globalConfig');
//
// 「$git grep globalConfig.resourcesdir」のように探すことで利用箇所を探すことができます。
//
module.exports = function() {

    var globalConfig = {
        app: 'app',
        resourcesdir: 'app/dist/public/resources',
        importdir: "node_modules",
        distdir: 'app/dist',
        srcserverdir: './server',
        srcdir: 'app/src',
        tmpdir: '.tmp',
        srcfiles: {
            js: ['./app/src/**/*.js'],
            tpl: ['./app/src/**/*.tpl.html']
        }
    };

    return globalConfig;
}
