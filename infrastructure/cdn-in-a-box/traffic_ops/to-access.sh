#!/usr/bin/env bash
#
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
#
# Defines bash functions to consistently interact with the Traffic Ops API
#
# Build FQDNs
export CDN_FQDN="$CDN_SUBDOMAIN.$TLD_DOMAIN"
export INFRA_FQDN="$INFRA_SUBDOMAIN.$TLD_DOMAIN"
export DB_FQDN="$DB_SERVER.$INFRA_FQDN"
export DNS_FQDN="$DNS_SERVER.$INFRA_FQDN"
export EDGE_FQDN="$EDGE_HOST.$INFRA_FQDN"
export MID_01_FQDN="$MID_01_HOST.$INFRA_FQDN"
export MID_02_FQDN="$MID_02_HOST.$INFRA_FQDN"
export ORIGIN_FQDN="$ORIGIN_HOST.$INFRA_FQDN"
export SMTP_FQDN="$SMTP_HOST.$INFRA_FQDN"
export TO_FQDN="$TO_HOST.$INFRA_FQDN"
export TM_FQDN="$TM_HOST.$INFRA_FQDN"
export TP_FQDN="$TP_HOST.$INFRA_FQDN"
export TR_FQDN="$TR_HOST.$INFRA_FQDN"
export TS_FQDN="$TS_HOST.$INFRA_FQDN"
export TV_FQDN="$TV_HOST.$INFRA_FQDN"

export TO_URL=${TO_URL:-https://$TO_FQDN:$TO_PORT}
export TO_USER=${TO_USER:-$TO_ADMIN_USER}
export TO_PASSWORD=${TO_PASSWORD:-$TO_ADMIN_PASSWORD}

export TO_API_VERSION=${TO_API_VERSION:-"3.0"}

export CURLOPTS=${CURLOPTS:--LfsS}
export CURLAUTH=${CURLAUTH:--k}
export COOKIEJAR=$(mktemp)

export MY_HOSTNAME="$(hostname -s)"

login=$(mktemp)

cleanup() {
	rm -f "$COOKIEJAR" "$login"
}

trap cleanup EXIT

cookie_current() {
	local cookiefile=$1
	[[ -s $cookiefile ]] || return 1

	# get expiration from cookiejar -- compare to current time
	exp=$(awk '/mojolicious/ {print $5}' $cookiefile | tail -n 1)
	cur=$(date +%s)

	# return value is the comparison itself
	(( $exp > $cur ))
}

to-auth() {

	# These are required
	# $TO_URL, $TO_USER, $TO_PASSWORDが設定されていなければ、エラーにする
	if [[ -z $TO_URL || -z $TO_USER || -z $TO_PASSWORD ]]; then
		echo TO_URL TO_USER TO_PASSWORD must all be set
		return 1
	fi

	# if cookiejar is current, nothing to do..
	cookie_current $COOKIEJAR && return

	# 認証用のcookie情報を取得するために「/api/3.0/user/login (POST)」にアクセスします。
	local url=$TO_URL/api/$TO_API_VERSION/user/login
	local datatype='Accept: application/json'
	cat >"$login" <<-CREDS
{ "u" : "$TO_USER", "p" : "$TO_PASSWORD" }
CREDS
	res=$(curl $CURLAUTH $CURLOPTS -H "$datatype" --cookie "$COOKIEJAR" --cookie-jar "$COOKIEJAR" -X POST --data @"$login" "$url")
	if [[ $res != *"Successfully logged in."* ]]; then
		echo "Login failed: $res"
		return 1
	fi
}

tv-ping() {
	# 認証が必要なエンドポイント「/api/3.0/vault/ping (GET)」へとアクセスします
	to-auth && \
		curl $CURLAUTH $CURLOPTS --cookie "$COOKIEJAR" -X GET "$TO_URL/api/$TO_API_VERSION/vault/ping"
}

to-ping() {
	# ping endpoint does not require authentication
	# 認証が不要なエンドポイント「/api/3.0/ping (GET)」へとアクセスします
	curl $CURLAUTH $CURLOPTS -X GET "$TO_URL/api/$TO_API_VERSION/ping"
}

to-get() {
	# 認証が必要な引数で指定されたエンドポイントに対してGETメソッドでアクセスします
	to-auth && \
		curl $CURLAUTH $CURLOPTS --cookie "$COOKIEJAR" -X GET "$TO_URL/$1"
}

# POSTエンドポイントへのリクエストを送る
# $1にはエンドポイントが、$2には「--data=」に指定されるPOSTのボディが指定される。$2は空、ファイル名指定、文字列指定の3種類に対応している
to-post() {

	local t
	local data

	if [[ -z "$2" ]]; then
		# $2が指定されない場合
		data=()
	elif [[ -f "$2" ]]; then
		# $2で指定された値がファイルの場合
		data=(--data "@${2}")
	else
		# $2に文字列が指定された場合には、mktempでファイル名を用意してそこに記述して、「--data」にはmktempのファイル名を指定する
		t=$(mktemp)
		echo "$2" >$t
		data=(--data "@${t}")
	fi

	# 認証が必要な引数で指定されたエンドポイントに対してPOSTメソッドでアクセスします
	to-auth && \
	    curl $CURLAUTH $CURLOPTS -H 'Content-Type: application/json;charset=UTF-8' --cookie "$COOKIEJAR" -X POST "${data[@]}" "$TO_URL/$1"
	[[ -n $t ]] && rm -f "$t"
}

# PUTエンドポイントへのリクエストを送る
to-put() {

	if [[ $# -lt 2 || -z "$2" ]]; then
		# 引数が2つよりも小さい または $2がゼロバイトの場合にはPUT時に指定するボディは空とする
		data=()
	elif [[ -f "$2" ]]; then
		# $2がファイルの場合には、そのまま「--data @<file>」として指定する
		data=(--data "@${2}")
	else
		# $2がそれ以外の文字列でコマンドライン上で指定する
		data=(--data "${2}")
	fi

	# 認証が必要なPUTエンドポイントへとリクエストする
	to-auth && \
	    curl $CURLAUTH $CURLOPTS --cookie "$COOKIEJAR" -X PUT "${data[@]}" "$TO_URL/$1"
}

# DELETEエンドポイントへのリクエストを送る
# $1にはエンドポイントが指定されます
to-delete() {
	# 認証が必要なDELETEエンドポイントへとリクエストする
	to-auth && \
		curl $CURLAUTH $CURLOPTS --cookie "$COOKIEJAR" -X DELETE "$TO_URL/$1"
}

# Constructs a server's JSON definiton and places it into the enroller's structure for loading
# args:
#         serverType - the type of the server to be created; one of "edge", "mid", "tm"
#         MY_CDN - the CDN name, default is "CDN-in-a-Box"
#         MY_CACHE_GROUP - the cache group, default is "CDN_in_a_Box_Edge"
#         MY_TCP_PORT - the tcp port, default is "80"
#         MY_HTTPS_PORT - the tcp port, default is "443"
to-enroll() {

	# Force fflush() on /shared
	sync

	# Wait for the initial data load to be copied
	# 下記のinitial-load-doneはTrafficOpsによりtraffic_ops/trafficops-init.sh中でtouchして生成されます
	until [[ -f "$ENROLLER_DIR/initial-load-done" ]] ; do
		echo "Waiting for enroller initial data load to complete...."
		sleep 2
		sync
	done

	# Wait for the Enroller servers directory to be created
	# このディレクトリ自体はenrollerが enroller/enroller.goのwatch()関数によってwatchとして登録した際に生成される。
	until [[ -d "${ENROLLER_DIR}/servers" ]] ; do
		echo "Waiting for ${ENROLLER_DIR}/servers ..."
		sleep 2
		sync
	done

	# If the servers dir vanishes, the docker shared volume isn't working right
	# もし /shared/enroller/serversがディレクトリではない場合には、何かおかしなことが起こっているので処理を終了する
	if [[ ! -d ${ENROLLER_DIR}/servers ]]; then
		echo "ERROR: ${ENROLLER_DIR}/servers not found -- contents:"
		find ${ENROLLER_DIR} -ls
		echo "ERROR: Halting Execution."
		tail -F /dev/null
	fi

	local serverType="$1"

	export MY_CDN="${2:-$CDN_NAME}"
	export MY_CACHE_GROUP="${3:-CDN_in_a_Box_Edge}"
	export MY_TCP_PORT="${4:-80}"
	export MY_HTTPS_PORT="${5:-443}"

	export MY_NET_INTERFACE='eth0'
	export MY_DOMAINNAME="$(dnsdomainname)"
	MY_IP="$(ifconfig $MY_NET_INTERFACE | grep 'inet ' | tr -s ' ' | cut -d ' ' -f 3)"
	export MY_IP="${MY_IP#"addr:"}/24"
	export MY_GATEWAY="$(route -n | grep $MY_NET_INTERFACE | grep -E '^0\.0\.0\.0' | tr -s ' ' | cut -d ' ' -f2)"
	MY_NETMASK="$(ifconfig $MY_NET_INTERFACE | grep 'inet ' | tr -s ' ' | cut -d ' ' -f 5)"
	export MY_NETMASK=${MY_NETMASK#"Mask:"}
	export MY_IP6_ADDRESS="$(ifconfig $MY_NET_INTERFACE | grep inet6 | grep -i global | sed 's/addr://' | awk '{ print $2 }')"
	if [[ "$MY_IP6_ADDRESS" != */64 ]]; then
		MY_IP6_ADDRESS="${MY_IP6_ADDRESS}/64"
	fi
	export MY_IP6_GATEWAY="$(route -n6 | grep UG | awk '{print $2}')"

	case "$serverType" in
		"db" )
			export MY_TYPE="TRAFFIC_OPS_DB"
			export MY_PROFILE="TRAFFIC_OPS_DB"
			export MY_STATUS="ONLINE"
			;;
		"dns" )
			export MY_TYPE="BIND"
			export MY_PROFILE="BIND_ALL"
			export MY_STATUS="ONLINE"
			;;
		"enroller" )
			export MY_TYPE="ENROLLER"
			export MY_PROFILE="ENROLLER_ALL"
			export MY_STATUS="ONLINE"
			;;
		"edge" )
			export MY_TYPE="EDGE"
			export MY_PROFILE="EDGE_TIER_ATS_CACHE"
			export MY_STATUS="REPORTED"
			;;
		"mid" )
			export MY_TYPE="MID"
			export MY_PROFILE="MID_TIER_ATS_CACHE"
			export MY_STATUS="REPORTED"
			;;
		"origin" )
			export MY_TYPE="ORG"
			export MY_PROFILE="ORG_MSO"
			export MY_STATUS="REPORTED"
			;;
		"tm" )
			export MY_TYPE="RASCAL"
			export MY_PROFILE="RASCAL-Traffic_Monitor"
			export MY_STATUS="ONLINE"
			;;
		"to" )
			export MY_TYPE="TRAFFIC_OPS"
			export MY_PROFILE="TRAFFIC_OPS"
			export MY_STATUS="ONLINE"
			;;
		"tr" )
			export MY_TYPE="CCR"
			export MY_PROFILE="TRAFFIC_ROUTER"
			export MY_STATUS="ONLINE"
			;;
		"tp" )
			export MY_TYPE="TRAFFIC_PORTAL"
			export MY_PROFILE="TRAFFIC_PORTAL"
			export MY_STATUS="ONLINE"
			;;
		"ts" )
			export MY_TYPE="TRAFFIC_STATS"
			export MY_PROFILE="TRAFFIC_STATS"
			export MY_STATUS="ONLINE"
			;;
		"tv" )
			export MY_TYPE="RIAK"
			export MY_PROFILE="RIAK_ALL"
			export MY_STATUS="ONLINE"
			;;
		"influxdb" )
			export MY_TYPE="INFLUXDB"
			export MY_PROFILE="INFLUXDB"
			export MY_STATUS="ONLINE"
			;;
		"grafana" )
			export MY_TYPE="GRAFANA"
			export MY_PROFILE="GRAFANA"
			export MY_STATUS="ONLINE"
			;;
		* )
			echo "Usage: to-enroll SERVER_TYPE" >&2
			echo "(SERVER_TYPE must be a recognized server type)" >&2
			return 1
			;;
	esac

	# replace env references in the file
	<"/server_template.json" envsubst | #first envsubst expands $MY_TCP_PORT and $MY_HTTPS_PORT so they are valid JSON
		jq '.cdnName = "$MY_CDN"' |
		if [[ -n "$MY_IP" && -n "$MY_GATEWAY" ]]; then
			jq '.interfaces[0].ipAddresses += [({} | .address = "$MY_IP" | .gateway = "$MY_GATEWAY" | .serviceAddress = true)]'
		else
			cat
		fi |
		if [[ -n "$MY_IP6_ADDRESS" && -n "$MY_IP6_GATEWAY" ]]; then
			jq '.interfaces[0].ipAddresses += [({} | .address = "$MY_IP6_ADDRESS" | .gateway = "$MY_IP6_GATEWAY" | .serviceAddress = true)]'
		else
			cat
		fi |
		envsubst >"${ENROLLER_DIR}/servers/$HOSTNAME.json"

	sleep 3
}

# Tests that this server exists in Traffic Ops
function testenrolled() {
	local tmp="$(to-get	'api/'$TO_API_VERSION'/servers?hostName='$MY_HOSTNAME'')"
	tmp=$(echo $tmp | jq '.response[]|select(.hostName=="'"$MY_HOSTNAME"'")')
	echo "$tmp"
}

# Add SSL keys
# args:
#     cdn_name
#     deliveryservice_name
#     hostname
#     crt_path
#     csr_path
#     key_path
to-add-sslkeys() {
	ds_crt="$(sed -n -e '/-----BEGIN CERTIFICATE-----/,$p' $4 | jq -s -R '.')"
	ds_csr="$(sed -n -e '/-----BEGIN CERTIFICATE REQUEST-----/,$p' $5 | jq -s -R '.')"
	ds_key="$(sed -n -e '/-----BEGIN PRIVATE KEY-----/,$p' $6 | jq -s -R '.')"
	json_request=$(jq -n \
	                  --arg     cdn        "$1" \
	                  --arg     dsname     "$2" \
	                  --arg     hostname   "$3" \
	                  --argjson crt        "$ds_crt" \
	                  --argjson csr        "$ds_csr" \
	                  --argjson key        "$ds_key" \
	                 "{ cdn: \$cdn,
	                    certificate: {
	                      crt: \$crt,
	                      csr: \$csr,
	                      key: \$key
	                    },
	                    deliveryservice: \$dsname,
	                    hostname: \$hostname,
	                    key: \$dsname,
	                    version: 1
	                 }")

	while true; do
		json_response=$(to-post 'api/'$TO_API_VERSION'/deliveryservices/sslkeys/add' "$json_request")
		if [[ -n "$json_response" ]] ; then
			sleep 3
			cdn_sslkeys_response=$(to-get "api/$TO_API_VERSION/cdns/name/$1/sslkeys" | jq '.response | length')
			if ((cdn_sslkeys_response>0)); then
				break
			else
				# Submit it again because the first time doesn't work !
				sleep 3
			fi
		else
			sleep 3
		fi
	done
}

# AUTO_SNAPQUEUE
# args:
#     expected_servers - should be a comma delimited list of expected docker service names to be enrolled
#     cdn_name
#
# TrafficOpsから起動される。
# 「to-auto-snapqueue "$AUTO_SNAPQUEUE_SERVERS" "$CDN_NAME"」として起動される
#  上記はCIABでは「to-auto-snapqueue "trafficops,trafficmonitor,trafficrouter,edge,mid-01,mid-02" "CDN-in-a-Box"」としてデフォルトでは指定されます。
to-auto-snapqueue() {

	while true; do

		# AUTO_SNAPQUEUE_SERVERS should be a comma delimited list of expected docker service names to be enrolled - see varibles.env

		# (例) $ echo "trafficops,trafficmonitor,trafficrouter,edge,mid-01,mid-02" | tr ',' '\n' | jq -R . | jq -M -c -e -s '.|sort'
		#      ["edge","mid-01","mid-02","trafficmonitor","trafficops","trafficrouter"]
		expected_servers_json=$(echo "$1" | tr ',' '\n' | jq -R . | jq -M -c -e -s '.|sort')

		# (例) $ jq -r -n --argjson expected '["edge","mid-01","mid-02","trafficmonitor","trafficops","trafficrouter"]' '$expected|join(",")'
		#      edge,mid-01,mid-02,trafficmonitor,trafficops,trafficrouter
		expected_servers_list=$(jq -r -n --argjson expected "$expected_servers_json" '$expected|join(",")')

		# (例) $ jq -r -n --argjson expected '["edge","mid-01","mid-02","trafficmonitor","trafficops","trafficrouter"]' '$expected|length'
		#      6
		expected_servers_total=$(jq -r -n --argjson expected "$expected_servers_json" '$expected|length')

		# 「api/3.0/servers(GET)」ヘとアクセスする
		# サンプル出力
		# (例) $ curl -k "https://localhost/api/3.0/servers" -b ./cookie.txt | jq -c -e '[.response[] | .hostName] | sort'
		#      ["edge","enroller","influxdb","mid-01","mid-02","origin","trafficmonitor","trafficops"]
		current_servers_json=$(to-get 'api/'$TO_API_VERSION'/servers' 2>/dev/null | jq -c -e '[.response[] | .hostName] | sort')

		# $current_servers_jsonがゼロバイトならば current_servers_jsonを[]として扱う
		[ -z "$current_servers_json" ] && current_servers_json='[]'

		# (例) $ jq -r -n --argjson current '["edge","enroller","influxdb","mid-01","mid-02","origin","trafficmonitor","trafficops"]' '$current|join(",")'
		#      edge,enroller,influxdb,mid-01,mid-02,origin,trafficmonitor,trafficops
		current_servers_list=$(jq -r -n --argjson current "$current_servers_json" '$current|join(",")')

		# (例)$ jq -r -n --argjson current '["edge","enroller","influxdb","mid-01","mid-02","origin","trafficmonitor","trafficops"]' '$current|length'
		#     8
		current_servers_total=$(jq -r -n --argjson current "$current_servers_json" '$current|length')

		# expectedに指定した値から、currrentのものを差し引いて残ったコンポーネントを表示している
		# (例) $ jq -n --argjson expected '["edge","mid-01","mid-02","trafficmonitor","trafficops","trafficrouter"]' --argjson current '["edge","enroller","influxdb","mid-01","mid-02","origin","trafficmonitor","trafficops"]' '$expected-$current'
		#      [
		#        "trafficrouter"
		#      ]
		remain_servers_json=$(jq -n --argjson expected "$expected_servers_json" --argjson current "$current_servers_json" '$expected-$current')

		# (例) $ jq -r -n --argjson remain '["trafficrouter"]' '$remain|join(",")'
		#      trafficrouter
		remain_servers_list=$(jq -r -n --argjson remain "$remain_servers_json" '$remain|join(",")')

		# (例) $ jq -r -n --argjson remain '["trafficrouter"]' '$remain|length'
		#      1
		remain_servers_total=$(jq -r -n --argjson remain "$remain_servers_json" '$remain|length')

		echo "AUTO-SNAPQUEUE - Expected Servers ($expected_servers_total): $expected_servers_list"
		echo "AUTO-SNAPQUEUE - Current Servers ($current_servers_total): $current_servers_list"
		echo "AUTO-SNAPQUEUE - Remain Servers ($remain_servers_total): $remain_servers_list"

		if ((remain_servers_total == 0)) ; then

			echo "AUTO-SNAPQUEUE - All expected servers enrolled."

			# デフォルトで2秒待つ
			sleep $AUTO_SNAPQUEUE_ACTION_WAIT

			echo "AUTO-SNAPQUEUE - Do automatic snapshot..."

			# 「api/3.0/cdns?name=CDN-in-a-Box (GET)」が実行される
			# 以下、サンプル
			# $ curl -k "https://localhost/api/3.0/cdns?name=CDN-in-a-Box" -b ./cookie.txt
			# {"response":[{"dnssecEnabled":false,"domainName":"mycdn.ciab.test","id":2,"lastUpdated":"2023-05-06 23:32:42+00","name":"CDN-in-a-Box"}]}
			# $ curl -s -k "https://localhost/api/3.0/cdns?name=CDN-in-a-Box" -b ./cookie.txt |jq '.response[0].id'
			# 2
			cdn_id=$(to-get "api/$TO_API_VERSION/cdns?name=$2" |jq '.response[0].id')

			# 「api/3.0/snapshot?cdnID=<cdn_id> (PUT)が実行される。ここでsnapshotが実行されているものと思われる
			# (サンプル)$ curl -s -k "https://localhost/api/3.0/snapshot?cdnID=2" -b ./cookie.txt -X PUT
			#           {"response":"SUCCESS"}
			to-put "api/$TO_API_VERSION/snapshot?cdnID=$cdn_id"

			# デフォルトで2秒待つ
			sleep $AUTO_SNAPQUEUE_ACTION_WAIT

			echo "AUTO-SNAPQUEUE - Do queue updates..."

			# 「api/3.0/cdns/<cdn_id>/queue_update (POST)」として指定される。
			# ここでQueue Updateが実行されていて、これが実行されるとキャッシュサーバ側のt3c applyで更新処理が走るようになる。
			to-post "api/$TO_API_VERSION/cdns/$cdn_id/queue_update" '{"action":"queue"}'
			break
		fi

		# デフォルトは2秒sleepする
		sleep $AUTO_SNAPQUEUE_POLL_INTERVAL

	done

}

check-skips() {
	if [[ "$SKIP_TRAFFIC_OPS_DATA" == true ]]; then
		touch /shared/SKIP_TRAFFIC_OPS_DATA
	fi
	if [[ "$SKIP_DIG_IP" == true ]]; then
		touch /shared/SKIP_DIG_IP
	fi
	sync
}
