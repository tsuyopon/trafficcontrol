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

# Required env vars
# Check that env vars are set
set -ex
for v in TO_HOST TO_PORT TO_ADMIN_USER TO_ADMIN_PASSWORD; do
    [[ -z $(eval echo \$$v) ]] || continue
    echo "$v is unset"
    exit 1
done

. /to-access.sh

TO_URL="https://$TO_FQDN:$TO_PORT"
# wait until the ping endpoint succeeds
while ! to-ping 2>/dev/null; do
   echo waiting for trafficops
   sleep 3
done

# NOTE: order dependent on foreign key references, e.g. profiles must be loaded before parameters
# 下記の順番で/shared/enroller/<xxxx>配下に設定ファイルを作成する
endpoints="cdns types divisions regions phys_locations tenants users cachegroups profiles parameters server_capabilities servers topologies deliveryservices federations server_server_capabilities deliveryservice_servers deliveryservices_required_capabilities"

# envsubstで標準入力されたテンプレートでそのテンプレート内部の文字列を置換するには 「envsubst $HOGE1 $HOGE2 < template」のようにする(envsubstに引数を与えないことも可能)
# ここでは $HOGE1や$HOGE2に相当する部分を取得しようとしている
# cf. https://qiita.com/minamijoyo/items/63ae57b99d4a4c5d7987
vars=$(awk -F = '/^\w/ {printf "$%s ",$1}' /variables.env)

waitfor() {

    # 第1引数
    local endpoint="$1"; shift

    # 第2引数
    local field="$1"; shift

    # 第3引数
    local value="$1"; shift

    # 第4引数 (リクエスト元を見ると第４引数は指定されない場合もある)
    local responseField="$1"

    if [[ -z "$responseField" ]]; then
      # 第4引数が渡されない場合には、第２引数の値をresponseFieldとして上書きする
      responseField="$field"
    else
      # 第4引数が渡された場合にはshiftする
      shift
    fi

    # 手前の分岐でshiftが実行されたかどうかで第４引数か、第５引数を取得するのかが変わってくる
    local additionalQueryString="$1"
    if [[ -n "$additionalQueryString" ]]; then
      shift
    fi

    while true; do
        # TO_API_VERSIONは明示的に指定されない限りto-access.shにいおいて3.0になる。
        v="$(to-get "api/${TO_API_VERSION}/${endpoint}?${field}=${value}${additionalQueryString}" | jq -r --arg field "$responseField" '.response[][$field]')";
        if [[ "$v" == "$value" ]]; then
          break
        fi
        echo "waiting for $endpoint $field=$value"
        sleep 3
    done

}

# special cases -- any data type requiring specific data to already be available in TO should have an entry here.
# e,g. deliveryservice_servers requires both deliveryservice and all servers to be available
delayfor() {

    # 「/trafffic_ops_data/$d/*.json」でマッチした1つのファイル名
    local f="$1"

    #  変数 f の値から、最後のスラッシュ / 以降の文字列を取り除いた結果を、変数 d に代入します。つまり、$dは$fのディレクトリ部分を示します(「/trafffic_ops_data/$d/」の部分)
    local d="${f%/*}"

    case $d in
        deliveryservice_servers)
            # $fのファイルサンプルは infrastructure/cdn-in-a-box/traffic_ops_data/deliveryservice_servers/020-demo1.json を確認のこと
            #
            # サンプル
            #   $ jq -r .xmlId < infrastructure/cdn-in-a-box/traffic_ops_data/deliveryservice_servers/020-demo1.json 
            #   demo1
            #   $ jq -r .serverNames[] < infrastructure/cdn-in-a-box/traffic_ops_data/deliveryservice_servers/020-demo1.json 
            #   origin
            ds=$( jq -r .xmlId <"$f" )
            waitfor deliveryservices xmlId "$ds"
            for s in $( jq -r .serverNames[] <"$f" ); do
                waitfor servers hostName "$s"
            done
            ;;
        topologies)
            # $fのファイルサンプルは infrastructure/cdn-in-a-box/traffic_ops_data/topologies/010-CDN_in_a_Box_Topology.json  を確認のこと
            #
            # サンプル
            #     $ jq -r '.nodes[] | .cachegroup' <infrastructure/cdn-in-a-box/traffic_ops_data/topologies/010-CDN_in_a_Box_Topology.json
            #     CDN_in_a_Box_Edge
            #     CDN_in_a_Box_Mid-01
            #     CDN_in_a_Box_Mid-02
            #     CDN_in_a_Box_Origin
            for cachegroup_name in $(jq -r '.nodes[] | .cachegroup' <"$f"); do
              waitfor cachegroups name "$cachegroup_name"
              cachegroup="$(to-get "api/${TO_API_VERSION}/cachegroups?name=${cachegroup_name}")"
              cachegroup_id="$(<<<"$cachegroup" jq '.response[] | .id')"
              cachegroup_type="$(<<<"$cachegroup" jq -r '.response[] | .typeName')"
              waitfor servers cachegroup "$cachegroup_id" cachegroupId "&type=${cachegroup_type%_LOC}"
            done
            ;;
    esac
}

load_data_from() {

    # /traffic_ops_dataディレクトリがなければエラー(基本的にdocker生成時にコピーされているはず)
    local dir="$1"
    if [[ ! -d $dir ]] ; then
        echo "Failed to load data from '$dir': directory does not exist"
    fi

    # /traffic_ops_dataに移動する
    cd "$dir"

    local status=0
    local has_ds_servers=''

    # デフォルトだと「deliveryservice_servers/020-demo1.json」というファイルが存在しているはず
    if ls deliveryservice_servers/'*.json'; then
      has_ds_servers='true'
    fi

    # この$endpointsには/shared/enroller配下に配置される予定のディレクトリ一覧が含まれています
    for d in $endpoints; do

        # Let containers know to write out server.json
        # TODO: なぜtopologiesになったらinitial-load-doneを書き込んでいるのかが不明
        if [[ "$d" = 'topologies' ]]; then
           touch "$ENROLLER_DIR/initial-load-done"
           sync
        fi

        # deliveryservicesが指定されたら、Traffic Vaultにアクセスできるまで待つ
        if [[ "$d" = 'deliveryservices' ]]; then
        	# Traffic Vault must be accepting connections before enroller can start
          until tv-ping; do
            echo "Waiting for Traffic Vault to accept connections"
            sleep 5
          done
        fi

        # [[と]]は[や]と違ってコマンドとして扱われることになります。 その後の「||」は左側のコマンドが失敗した場合に右側のコマンドが実行されるORです。
        # つまり、下記では変数$dがディレクトリで"ない"場合に繰り返し(continue)するということを意図しています。
        [[ -d $d ]] || continue

        # 「/trafffic_ops_data/$d/*.json」があるかのを抽出して、ファイル毎に処理を行なっています
        for f in $(find "$d" -name "*.json" -type f); do
            # 上記の$fにはファイル名だけではなく、ディレクトリも一部含まれていることに注意すること
            # (fileの実行サンプル)
            #   $ find federations -name "*.json" -type f
            #   federations/010-foo.kabletown.net.json
            echo "Loading $f"

            # *.jsonで見つかったファイル形式がfileではなかったらcontinueする
            if [ ! -f "$f" ]; then
              echo "No such file: $f" >&2;
              continue;
            fi

            # 現在の登録状態をチェックして、その状態に応じてwaitする関数
            delayfor "$f"

            # envsubstコマンドに「/trafffic_ops_data/$d/*.json」を標準入力として与えて、
            # envsubstコマンドには引数として$varsで指定された文字列のみを置換するように指示する。その後「$ENROLLER_DIR/$f」(/shared/enroller/$f)へと書き出す ($fには一部ディレクトリ情報も含まれることに注意すること)
            #
            # (重要) TrafficOps側でここで書き込みされたこの情報を元にしてenroller側ではファイルを検知して、TrafficOpsのAPIに初期登録作業が行われることになる。
            envsubst "$vars" <"$f"  > "$ENROLLER_DIR/$f"

            # 上記でtraffic_ops_data中に書き込んだ後なので、ボリュームの同期を行なわせる($ENROLLER_DIRは他のコンポーネントでもmountしているため)
            sync

        done

    done

    # $statusが変わっていなければOK。変わっていたら処理を終了させる
    if [[ $status -ne 0 ]]; then
        exit $status
    fi

    # 直前のディレクトリに戻る
    cd -
}

# If Traffic Router debugging is enabled, keep zone generation from timing out
# (for 5 minutes), in case that is what you are debugging
traffic_router_zonemanager_timeout() {
  if [[ "$TR_DEBUG_ENABLE" != true ]]; then
    return;
  fi;

  local modified_crconfig crconfig_path zonemanager_timeout;
  crconfig_path=/traffic_ops_data/profiles/040-TRAFFIC_ROUTER.json;
  modified_crconfig="$(mktemp)";
  # 5 minutes, which is the default zonemanager.cache.maintenance.interval value
  zonemanager_timeout="$(( 60 * 5 ))";
  jq \
    --arg zonemanager_timeout $zonemanager_timeout \
    '.params = .params + [{"configFile": "CRConfig.json", "name": "zonemanager.init.timeout", "value": $zonemanager_timeout}]' \
    <$crconfig_path >"$modified_crconfig";
  mv "$modified_crconfig" $crconfig_path;
}

# /shared/SKIP_TRAFFIC_OPS_DATAが配置される場合には/shared/enroller配下に/traffic_ops_dataのファイルは配置されないので、基本的な初期設定情報は同期されない。
# デフォルトでは/shared/SKIP_TRAFFIC_OPS_DATAは存在しない方のコードパスが実行される
if [[ ! -e /shared/SKIP_TRAFFIC_OPS_DATA ]]; then
	traffic_router_zonemanager_timeout

	# Load required data at the top level
    # ここでは/traffic_ops_dataのファイル内容を一部envsubstで置換の上で/shared/enrollerに配置している。つまり、ここを起点にenrollerのファイル検知処理が動き出す。」
	# この/traffic_ops_dataにあるファイルの実態は、infrastructure/cdn-in-a-box/traffic_ops_dataからコピーされたファイルが存在している
	load_data_from /traffic_ops_data

else

	touch "$ENROLLER_DIR/initial-load-done"
fi
