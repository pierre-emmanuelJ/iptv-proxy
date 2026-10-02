/*
 * Iptv-Proxy is a project to proxyfie an m3u file and to proxyfie an Xtream iptv service (client API).
 * Copyright (C) 2020  Pierre-Emmanuel Jacquier
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

// Package xtream holds what the proxy needs to know about the Xtream Codes
// client API. Provider answers are never decoded into fixed structures:
// providers disagree on the type of almost every field, so answers are passed
// on as they are and only the parts naming the provider are rewritten.
package xtream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Account is one side of the proxy: where an Xtream service is reached and
// with which credentials.
type Account struct {
	// BaseURL is "scheme://host[:port][/path]" without a trailing slash.
	BaseURL  string
	User     string
	Password string
}

// StreamPrefixes are the path prefixes Xtream uses in front of
// "<user>/<password>/<id>" for its streams ("" being the live short form).
var StreamPrefixes = []string{"", "live/", "movie/", "series/", "timeshift/"}

// APIURL builds the address of an endpoint ("player_api.php", "get.php",
// "xmltv.php") on the account, with its credentials and the given parameters.
// Any "username" or "password" in params is ignored.
func (a Account) APIURL(endpoint string, params url.Values) string {
	q := url.Values{}
	q.Set("username", a.User)
	q.Set("password", a.Password)
	for k, vs := range params {
		if k == "username" || k == "password" {
			continue
		}
		q[k] = vs
	}
	return fmt.Sprintf("%s/%s?%s", strings.TrimRight(a.BaseURL, "/"), endpoint, q.Encode())
}

// StreamURL builds the address of a stream: "<base>/<prefix><user>/<password>/<rest>".
func (a Account) StreamURL(prefix, rest string) string {
	return fmt.Sprintf("%s/%s%s/%s/%s", strings.TrimRight(a.BaseURL, "/"), prefix, url.PathEscape(a.User), url.PathEscape(a.Password), rest)
}

// jsonEscaped is s as PHP's json_encode writes it by default: "/" as "\/".
func jsonEscaped(s string) string {
	return strings.ReplaceAll(s, "/", `\/`)
}

// Sanitize rewrites, in a provider answer, whatever names the provider
// account into its proxy equivalent, so that the provider's credentials never
// reach a client:
//   - stream addresses ("<provider>/movie/<user>/<password>/1.mp4") become the
//     same stream on the proxy;
//   - any other "/<user>/<password>/" path and "username=…&password=…" query
//     gets the proxy's credentials.
//
// Other provider addresses (logos, covers) are left alone: the proxy does not
// serve them. Both plain and JSON-escaped ("\/") slashes are handled.
func Sanitize(body []byte, provider, proxy Account) []byte {
	if provider.User == "" || provider.Password == "" {
		return body
	}

	var pairs []string
	add := func(from, to string) {
		if from == to {
			return
		}
		pairs = append(pairs, from, to, jsonEscaped(from), jsonEscaped(to))
	}

	for _, escape := range []func(string) string{url.PathEscape, func(s string) string { return s }} {
		from := "/" + escape(provider.User) + "/" + escape(provider.Password) + "/"
		to := "/" + escape(proxy.User) + "/" + escape(proxy.Password) + "/"
		for _, prefix := range StreamPrefixes {
			add(strings.TrimRight(provider.BaseURL, "/")+"/"+prefix+from[1:], strings.TrimRight(proxy.BaseURL, "/")+"/"+prefix+to[1:])
		}
		add(from, to)
	}
	for _, sep := range []string{"&", "&amp;"} {
		add(
			"username="+url.QueryEscape(provider.User)+sep+"password="+url.QueryEscape(provider.Password),
			"username="+url.QueryEscape(proxy.User)+sep+"password="+url.QueryEscape(proxy.Password),
		)
	}

	return []byte(strings.NewReplacer(pairs...).Replace(string(body)))
}

// ProxyInfo is how the proxy presents itself in a login answer.
type ProxyInfo struct {
	Account  Account
	Hostname string
	Port     int
	Protocol string // "http" or "https"
}

// RewriteLogin turns the provider's login answer into the proxy's: same
// account state (status, expiry, connections...), but the proxy's credentials
// and address. Fields the proxy does not know are kept. An answer that is not
// the expected JSON object is returned unchanged with ok false.
func RewriteLogin(body []byte, info ProxyInfo) (out []byte, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // keep numbers as the provider wrote them
	var answer map[string]any
	if err := dec.Decode(&answer); err != nil || answer == nil {
		return body, false
	}

	if user, isObject := answer["user_info"].(map[string]any); isObject {
		user["username"] = info.Account.User
		user["password"] = info.Account.Password
	}
	if server, isObject := answer["server_info"].(map[string]any); isObject {
		server["url"] = info.Protocol + "://" + info.Hostname
		server["server_protocol"] = info.Protocol
		// A port is written the way the provider wrote it, string or
		// number: that is what the provider's clients are known to read.
		port := json.Number(strconv.Itoa(info.Port))
		for _, field := range []string{"port", "https_port", "rtmp_port"} {
			if _, isNumber := server[field].(json.Number); isNumber {
				server[field] = port
			} else {
				server[field] = port.String()
			}
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(answer); err != nil {
		return body, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

// Text reads a JSON value that providers write either as a string or as a
// number (ids, mostly): 12, "12" and 12.0 all give "12".
func Text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		if f, err := t.Float64(); err == nil && f == float64(int64(f)) && !strings.ContainsAny(t.String(), "eE") {
			return strconv.FormatInt(int64(f), 10)
		}
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

// DecodeList reads a provider list (categories, streams). Some providers
// answer with an object keyed by index instead of an array, and with an
// empty object, "null" or "false" for an empty list.
func DecodeList(body []byte) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding provider list: %w", err)
	}

	var items []map[string]any
	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			if m, isObject := item.(map[string]any); isObject {
				items = append(items, m)
			}
		}
	case map[string]any:
		if _, isLogin := list["user_info"]; isLogin {
			return nil, fmt.Errorf("the provider answered with its login page instead of a list")
		}
		keys := make([]string, 0, len(list))
		for k := range list {
			keys = append(keys, k)
		}
		sortNumeric(keys)
		for _, k := range keys {
			if m, isObject := list[k].(map[string]any); isObject {
				items = append(items, m)
			}
		}
	}
	return items, nil
}

func sortNumeric(keys []string) {
	less := func(a, b string) bool {
		x, errX := strconv.Atoi(a)
		y, errY := strconv.Atoi(b)
		if errX == nil && errY == nil {
			return x < y
		}
		return a < b
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && less(keys[j], keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}
