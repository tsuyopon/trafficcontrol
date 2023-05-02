package poller

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

import (
	"time"
)

const PollerTypeNOOP = "noop"

// golangではinit関数はパッケージインポート時に明示的に実行を指定しなくても実行されます。つまり、下記のinitは読み込み時に実行されます。
// 注意点として、同じパッケージ内に複数のinit()関数がある場合、実行の順序が保証されません。また、同じパッケージを複数回インポートしても、init()関数は1回しか実行されません。
func init() {
	AddPollerType(PollerTypeNOOP, nil, nil, noopPoll)
}

func noopPoll(ctx interface{}, url string, host string, pollID uint64) ([]byte, time.Time, time.Duration, error) {
	return nil, time.Now(), 0, nil
}
