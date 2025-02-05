#!/usr/bin/env bash
# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

INIT_DIR="/etc/init.d"

set-dns.sh
insert-self-into-dns.sh

source /to-access.sh

# Wait on SSL certificate generation
# X509_CA_ENV_FILE = /shared/ssl/environment ファイルが生成されるまで待つ (これはTrafficOpsによって生成される)
until [[ -f "$X509_CA_ENV_FILE" ]]
do
  echo "Waiting on Shared SSL certificate generation"
  sleep 3
done

# Source the CIAB-CA shared SSL environment
# X509_GENERATION_COMPLETEの環境変数は、traffic_ops/run-go.shの中でechoして直接して設定される
until [[ -n "$X509_GENERATION_COMPLETE" ]]
do
  echo "Waiting on X509 vars to be defined"
  sleep 1
  source "$X509_CA_ENV_FILE"
done

# Trust the CIAB-CA at the System level
cp $X509_CA_CERT_FULL_CHAIN_FILE /etc/pki/ca-trust/source/anchors
update-ca-trust extract

# Configuration of Traffic Portal
key=$X509_INFRA_KEY_FILE
cert=$X509_INFRA_CERT_FILE
ca=/etc/pki/tls/certs/ca-bundle.crt

# set configs to point to TO_FQDN
sed -i -e "/^\s*base_url:/ s@'.*'@'https://$TO_FQDN:$TO_PORT/api/'@" /etc/traffic_portal/conf/config.js
sed -i -e "/^\s*cert:/ s@'.*'@'$cert'@" /etc/traffic_portal/conf/config.js
sed -i -e "/^\s*key:/ s@'.*'@'$key'@" /etc/traffic_portal/conf/config.js

# 下記２つは後でjqによる変換操作を行う際のinput, outputファイルとなります。
props=/opt/traffic_portal/public/traffic_portal_properties.json
tmp=$(mktemp)

# ログとしてTrafficOpsの情報を出力しておく
echo "TO_HOST: $TO_HOST"
echo "TO_HOST: $TO_PORT"
echo "TO_FQDN: $TO_FQDN"


# $propsは入力ファイル、$tmpは出力ファイルを表します。入力を受け取ってjqで変換したら出力しています。
# $propsにて「properties.api.baseUrl」プロパティを「https://<TO_FQDN>:<TO_PORT>」の情報に置換しています。
jq --arg TO_FQDN "$TO_FQDN:$TO_PORT" '.properties.api.baseUrl = "https://"+$TO_FQDN' <$props >$tmp
mv $tmp $props

# Enroll the Traffic Portal
# TrafficPortalをenrollする。
to-enroll "tp" ALL || (while true; do echo "enroll failed."; sleep 3 ; done)

# Add node to the path for situations in which the environment is passed.
./$INIT_DIR/traffic_portal start

# コンテナが終了しないように/dev/nullをtail -fしておく
tail -f /dev/null
