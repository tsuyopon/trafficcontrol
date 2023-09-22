/*


 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

 http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.

 */

// npm依存パッケージのインストールや更新を行う
// see: https://www.npmjs.com/package/grunt-install-dependencies
module.exports = {
	options: {

		// "npm install"コマンドを実行するディレクトリを指定する
		cwd: '<%= globalConfig.distdir %>',

		// falseだとdevDependenciesをインストールしない。
		// https://github.com/ahutchings/grunt-install-dependencies/blob/416a47d7200151e3ba7aa818b44fc7799350fe2e/README.md#optionsisdevelopment
		isDevelopment: false
	}
};
