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


// AngularJSテンプレートをJavaScriptへと変換してくれる仕組みです。AngularJSに特化しています。
// 
// grunt-html2jsの使い方
//   see: https://github.com/rquadling/grunt-html2js

module.exports = {
    options: {
        base: './app/src'
    },
    'dist': {

        // 「find traffic_portal/ -name *.tpl.html」として引っかかるファイルが集約するHTMLテンプレートの対象となる
        src: ['<%= globalConfig.srcfiles.tpl %>'],

        //  srcの対象となったファイル全てを「.tmp/app-templates.js」に書き出す
        dest: '<%= globalConfig.tmpdir %>/app-templates.js',

        // 全体のモジュールを指定する定義がこれになる。
        // なお、「app.templates」自体はapp.jsからモジュールとして指定されていますので確認してみてください。
        module: 'app.templates'
    }
};


// 参考 
//
// html2jsによりtpl.htmlがどのようにJSへと変換されるのでしょうか?
// (snip)の部分は検知された大量のテンプレートを表していて、下記サンプルでは、上記のdist.moduleに指定された「app.templates」に登録される「common/directives/treeSelect/tree.select.tpl.html」と「modules/public/public.tpl.html」のみを表示しています。
/*
 * 
 * angular.module("app.templates", ["common/directives/treeSelect/tree.select.tpl.html", (snip), "modules/public/public.tpl.html"]);
 * 
 * angular.module("common/directives/treeSelect/tree.select.tpl.html", []).run(["$templateCache", function($templateCache) {
 *   $templateCache.put("common/directives/treeSelect/tree.select.tpl.html",
 *     "<!--\n" +
 *     "Licensed to the Apache Software Foundation (ASF) under one\n" +
 *     "or more contributor license agreements.  See the NOTICE file\n" +
 *     "distributed with this work for additional information\n" +
 *     "regarding copyright ownership.  The ASF licenses this file\n" +
 *     "to you under the Apache License, Version 2.0 (the\n" +
 *     "\"License\"); you may not use this file except in compliance\n" +
 *     "with the License.  You may obtain a copy of the License at\n" +
 *     "\n" +
 *     "  http://www.apache.org/licenses/LICENSE-2.0\n" +
 *     "\n" +
 *     "Unless required by applicable law or agreed to in writing,\n" +
 *     "software distributed under the License is distributed on an\n" +
 *     "\"AS IS\" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY\n" +
 *     "KIND, either express or implied.  See the License for the\n" +
 *     "specific language governing permissions and limitations\n" +
 *     "under the License.\n" +
 *     "-->\n" +
 *     "<div class=\"tree-select-root\" role=\"combobox\" aria-expanded=\"{{ scope.shown }}\">\n" +
 *     "    <input type=\"text\" id=\"{{ handle }}\" name=\"{{ handle }}\" class=\"form-control display-field\" ng-model=\"selected.label\" ng-click=\"toggle()\" required readonly>\n" +
 *     "    <div class=\"tree-drop-down\" ng-show=\"shown\">\n" +
 *     "        <div class=\"input-group search-box\">\n" +
 *     "            <label class=\"input-group-addon has-tooltip control-label\" for=\"{{ handle }}searchText\">\n" +
 *     "                Filter:\n" +
 *     "                <div class=\"helptooltip\">\n" +
 *     "                    <div class=\"helptext\">Fuzzy search by tenant name</div>\n" +
 *     "                </div>\n" +
 *     "            </label>\n" +
 *     "            <input id=\"{{ handle }}searchText\" name=\"{{ handle }}searchText\" type=\"search\" class=\"form-control\" ng-model=\"searchText\" maxlength=\"48\">\n" +
 *     "        </div>\n" +
 *     "        <ul class=\"nav nav-list nav-pills nav-stacked\">\n" +
 *     "            <li class=\"tree-row\" ng-repeat=\"row in treeRows | filter: checkFilters\">\n" +
 *     "                <button type=\"button\" ng-style=\"{'left': row.depth*8+'px'}\" ng-click=\"select(row)\" name=\"{{row.label}}\">\n" +
 *     "                    <div ng-click=\"collapse(row, $event)\"><i class=\"fa\" ng-class=\"getClass(row)\"></i></div>\n" +
 *     "                    <span>{{row.label}}</span>\n" +
 *     "                </button>\n" +
 *     "            </li>\n" +
 *     "        </ul>\n" +
 *     "    </div>\n" +
 *     "</div>\n" +
 *     "");
 * }]);
 * 
 * 
 * (snip)
 * 
 * angular.module("modules/public/public.tpl.html", []).run(["$templateCache", function($templateCache) {
 *   $templateCache.put("modules/public/public.tpl.html",
 *     "<!--\n" +
 *     "Licensed to the Apache Software Foundation (ASF) under one\n" +
 *     "or more contributor license agreements.  See the NOTICE file\n" +
 *     "distributed with this work for additional information\n" +
 *     "regarding copyright ownership.  The ASF licenses this file\n" +
 *     "to you under the Apache License, Version 2.0 (the\n" +
 *     "\"License\"); you may not use this file except in compliance\n" +
 *     "with the License.  You may obtain a copy of the License at\n" +
 *     "\n" +
 *     "  http://www.apache.org/licenses/LICENSE-2.0\n" +
 *     "\n" +
 *     "Unless required by applicable law or agreed to in writing,\n" +
 *     "software distributed under the License is distributed on an\n" +
 *     "\"AS IS\" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY\n" +
 *     "KIND, either express or implied.  See the License for the\n" +
 *     "specific language governing permissions and limitations\n" +
 *     "under the License.\n" +
 *     "-->\n" +
 *     "\n" +
 *     "<div id=\"publicContainer\">\n" +
 *     "    <div ui-view=\"publicContent\"></div>\n" +
 *     "</div>\n" +
 *     "");
 * }]);
 * 
*/
