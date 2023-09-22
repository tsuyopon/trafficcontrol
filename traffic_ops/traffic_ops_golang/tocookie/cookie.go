// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

// http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tocookie

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const GeneratedByStr = "trafficcontrol-go-tocookie"
const Name = "mojolicious"
const DefaultDuration = time.Hour

type Cookie struct {
	AuthData    string `json:"auth_data"`
	ExpiresUnix int64  `json:"expires"`
	By          string `json:"by"`
}

func checkHmac(message, messageMAC, key []byte) bool {
	mac := hmac.New(sha1.New, key)
	mac.Write(message)
	expectedMAC := mac.Sum(nil)
	return hmac.Equal(messageMAC, expectedMAC)
}

// Cookie情報を秘密鍵を用いて検証する
func Parse(secret, cookie string) (*Cookie, error) {

	// ログイン後のCookieとして送付されてくるサンプルを記載(一部改竄済み)
	// access_token=eyJhbGciOiJIUzI1NiIsIXXXXXIkpXVCJ9.eyJleHAiOjE2ODQyOTUzODIsIm1vam9Db29raWUiOiJleUpoZFhSb1gyUmhkR0VpT2lKaFpHMXBiaUlzSW1WNGNHbHlaWE1pT2pFMk9EUXlPVFV6T0RJc0ltSjVJam9pZEhKaFptWnBZMk52Ym5SeWIyd3RaMjh0ZEc5amIyOXJhV1VpZlEtLTVmZWZiYWRmZDA1YjUwNjBlNzNlMGEXXXXXYjJiZjUwNmVkODEyNWYifQ.G-R46yZlNzDI5uQTgXz-1gGy3Raud763ebAFENXXXXX; 
	// mojolicious=eyJhdXRoX2RhdGEiOiJhZG1pbiIsImV4cGlyZXMiOjE2ODQyNzczODIsImJ5IjoidHJhZmZpY2NvbnRyb2wtZ28tdG9jb23raWUifQ--0f8f04ed0e60ef14f4088426f2fc7a3a400b7c40; last_seen_log=2023-05-16T21:49:42.4752559Z

	dashPos := strings.Index(cookie, "-")
	if dashPos == -1 {
		return nil, fmt.Errorf("malformed cookie '%s' - no dashes", cookie)
	}

	lastDashPos := strings.LastIndex(cookie, "-")
	if lastDashPos == -1 {
		return nil, fmt.Errorf("malformed cookie '%s' - no dashes", cookie)
	}

	if len(cookie) < lastDashPos+1 {
		return nil, fmt.Errorf("malformed cookie '%s' -- no signature", cookie)
	}

	base64Txt := cookie[:dashPos]
	txtBytes, err := base64.RawURLEncoding.DecodeString(base64Txt)
	if err != nil {
		return nil, fmt.Errorf("error decoding base64 data: %v", err)
	}
	base64TxtSig := cookie[:lastDashPos-1] // the signature signs the base64 including trailing hyphens, but the Go base64 decoder doesn't want the trailing hyphens.

	base64Sig := cookie[lastDashPos+1:]
	sigBytes, err := hex.DecodeString(base64Sig)
	if err != nil {
		return nil, fmt.Errorf("error decoding signature: %v", err)
	}

	// cookieにつめられているのはJWT形式の値であり、最後の部分は署名であるので秘密鍵を使って検証する
	if !checkHmac([]byte(base64TxtSig), sigBytes, []byte(secret)) {
		return nil, fmt.Errorf("bad signature")
	}

	cookieData := Cookie{}
	if err := json.Unmarshal(txtBytes, &cookieData); err != nil {
		return nil, fmt.Errorf("error decoding base64 text '%s' to JSON: %v", string(txtBytes), err)
	}

	if cookieData.ExpiresUnix-time.Now().Unix() < 0 {
		return nil, fmt.Errorf("signature expired")
	}

	return &cookieData, nil
}

func NewRawMsg(msg, key []byte) string {
	base64Msg := base64.RawURLEncoding.EncodeToString(msg)
	mac := hmac.New(sha1.New, []byte(key))
	mac.Write([]byte(base64Msg))
	encMac := mac.Sum(nil)
	base64Sig := hex.EncodeToString(encMac)
	return base64Msg + "--" + base64Sig
}

func GetCookie(authData string, duration time.Duration, secret string) *http.Cookie {
	expiry := time.Now().Add(duration)
	maxAge := int(duration.Seconds())
	c := Cookie{By: GeneratedByStr, AuthData: authData, ExpiresUnix: expiry.Unix()}
	m, _ := json.Marshal(c)
	msg := NewRawMsg(m, []byte(secret))
	httpCookie := http.Cookie{Name: "mojolicious", Value: msg, Path: "/", Expires: expiry, MaxAge: maxAge, HttpOnly: true}
	return &httpCookie
}
