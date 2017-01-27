#!/bin/bash 
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.
#

. mysql-to-postgres.env

#Traffic Ops Settings
# The following configs should be configured to point to the 
# Traffic Ops instances that is connected to the MySQL that 
# you want to convert
separator="---------------------------------------"

function display_env() {

  echo "TO_SERVER: $TO_SERVER"
  echo "TO_USER: $TO_USER"
  echo "TO_PASSWORD: $TO_PASSWORD"
  echo "MYSQL_HOST: $MYSQL_HOST"
  echo "MYSQL_PORT: $MYSQL_PORT"
  echo "MYSQL_DATABASE: $MYSQL_DATABASE"
  echo "MYSQL_USER: $MYSQL_USER"
  echo "MYSQL_PASSWORD: $MYSQL_PASSWORD"

  echo "POSTGRES_HOST: $POSTGRES_HOST"
  echo "POSTGRES_PORT: $POSTGRES_PORT"
  echo "POSTGRES_DATABASE: $POSTGRES_DATABASE"
  echo "POSTGRES_USER: $POSTGRES_USER"
  echo "POSTGRES_PASSOWRD: $POSTGRES_PASSWORD"
  echo "PGDATA: $PGDATA"
  echo "PGDATA_VOLUME: $PGDATA_VOLUME"
  echo "PGLOGS_VOLUME: $PGLOGS_VOLUME"

}


function start_staging_mysql_server() {

  docker-compose -p trafficops -f mysql_host.yml down
  docker-compose -p trafficops -f mysql_host.yml up --build -d

  #Wait for MySQL to come up
  export WAITER_HOST=$MYSQL_HOST
  export WAITER_PORT=$MYSQL_PORT
  docker-compose -p trafficops -f waiter.yml up --build
  echo $separator
  echo "Mysql Host is started..."
  echo $separator

  #Wait for Postgres to come up
  export WAITER_HOST=$POSTGRES_HOST
  export WAITER_PORT=$POSTGRES_PORT
  docker-compose -p trafficops -f waiter.yml up --build
  echo $separator
  echo "Postgres Host is started..."
  echo $separator

}

function migrate_data_from_mysql_to_postgres() {

  echo $separator
  echo "Starting Mysql to Postgres Migration..."
  echo $separator
  docker-compose -p trafficops -f mysql-to-postgres.yml down
  docker-compose -p trafficops -f mysql-to-postgres.yml up --build
}


function run_postgres_datatypes_conversion() {
  echo $separator
  echo "Starting Mysql to Postgres Migration..."
  echo $separator
  docker-compose -p trafficops -f convert.yml up --build
}


function clean() {
  echo $separator
  echo "Cleaning up..."
  echo $separator
  docker kill trafficops_mysql_host_1
  docker-compose -p trafficops -f mysql-to-postgres.yml down --remove-orphans
  docker-compose -p trafficops -f convert.yml down --remove-orphans
  docker rm trafficops_mysql-to-postgres_1 
  docker rm trafficops_convert_1
  docker rm trafficops_mysql_host_1
  docker rmi trafficops_mysql-to-postgres
  docker rmi trafficops_convert 
  docker rmi trafficops_mysql_host
  docker rmi mysql:5.6 
  docker rmi dimitri/pgloader:latest
}

start_staging_mysql_server
#display_env
migrate_data_from_mysql_to_postgres
run_postgres_datatypes_conversion
clean
