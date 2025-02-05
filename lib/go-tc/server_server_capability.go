package tc

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

// ServerServerCapability represents an association between a server capability and a server.
type ServerServerCapability struct {

	// jsonでコードでは「json:"lastUpdated"」が優先され、ORMマッピングの場合には「db:"last_updated"」が優先されます。
	LastUpdated      *TimeNoMod `json:"lastUpdated" db:"last_updated"`

	// 下記の「omitempty」というのは特別な識別子であり出力時にその項目がなければ省略するというものです。ドキュメントにも記載されています。
	// https://pkg.go.dev/encoding/json#Marshal
	Server           *string    `json:"serverHostName,omitempty" db:"host_name"`
	ServerID         *int       `json:"serverId" db:"server"`
	ServerCapability *string    `json:"serverCapability" db:"server_capability"`
}

// ServerServerCapabilitiesResponse is the type of a response from Traffic
// Ops to a request made to its /server_server_capabilities.
type ServerServerCapabilitiesResponse struct {
	Response []ServerServerCapability `json:"response"`
	Alerts
}
