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


// see: https://qiita.com/sakamotoyuya/items/ae55d8d6cf464b84c9cd#grunt%E3%81%AE%E5%B0%8E%E5%85%A5
module.exports = {
    options: {
        includePaths: [
            "<%= globalConfig.importdir %>"
        ],
        quietDeps: true,
        precision: 10,
    },
    dev: {
        options: {
            sourceMap: true
        },
        files: [{
            expand: true,
            cwd: "<%= globalConfig.srcdir %>",
            src: ["styles/*.scss"],
            dest: "<%= globalConfig.resourcesdir %>",
            ext: ".css",
        }],
    },
    prod: {
        options: {
            outputStyle: "compressed"
        },
        files: [{
            expand: true,
            cwd: "<%= globalConfig.srcdir %>",
            src: ["styles/*.scss"],
            dest: "<%= globalConfig.resourcesdir %>",
            ext: ".css",
        }],
    }
};
