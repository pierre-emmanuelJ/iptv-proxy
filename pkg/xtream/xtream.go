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
	"html"
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

// APIEndpoints are the endpoints of the Xtream client API the proxy serves.
var APIEndpoints = []string{"player_api.php", "get.php", "xmltv.php"}

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
	// The provider's API (a guide address, typically) is the proxy's.
	for _, endpoint := range APIEndpoints {
		add(strings.TrimRight(provider.BaseURL, "/")+"/"+endpoint, strings.TrimRight(proxy.BaseURL, "/")+"/"+endpoint)
	}
	// Query parameters, in any order and whatever sits between them.
	add("password="+url.QueryEscape(provider.Password), "password="+url.QueryEscape(proxy.Password))
	add("username="+url.QueryEscape(provider.User), "username="+url.QueryEscape(proxy.User))

	return []byte(strings.NewReplacer(pairs...).Replace(string(body)))
}

// Scrub replaces every occurrence of the provider's user and password, in
// any of the forms an address or a page may carry them, by the proxy's. It
// is meant for what is not data: an error page of the provider, which may
// well repeat the address it was asked. (In data, a short password could be
// part of an id: Sanitize is the one to use there.)
func Scrub(body []byte, provider, proxy Account) []byte {
	var pairs []string
	seen := map[string]bool{}
	add := func(from, to string) {
		if from == "" || from == to || seen[from] {
			return
		}
		seen[from] = true
		pairs = append(pairs, from, to)
	}
	forms := []func(string) string{
		func(s string) string { return s },
		url.QueryEscape,
		url.PathEscape,
		html.EscapeString,
		func(s string) string { return html.EscapeString(url.QueryEscape(s)) },
	}
	// The password first: where both could match, the secret one wins.
	for _, form := range forms {
		add(form(provider.Password), form(proxy.Password))
	}
	for _, form := range forms {
		add(form(provider.User), form(proxy.User))
	}
	if len(pairs) == 0 {
		return body
	}
	return []byte(strings.NewReplacer(pairs...).Replace(string(body)))
}

// Leaks tells whether a text holds the provider's password in one of the
// forms Scrub knows.
func Leaks(text string, provider Account) bool {
	if provider.Password == "" {
		return false
	}
	for _, form := range []string{provider.Password, url.QueryEscape(provider.Password), url.PathEscape(provider.Password), html.EscapeString(provider.Password)} {
		if strings.Contains(text, form) {
			return true
		}
	}
	return false
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

// FilterList keeps the entries of a provider list (categories, streams) that
// keep accepts. Each kept entry is written exactly as the provider wrote it,
// and a list written as an object keyed by index stays one. An entry that is
// not an object is kept. What is not a list ("null", an error object) is
// returned as it is.
func FilterList(body []byte, keep func(entry map[string]any) bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	start, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("decoding provider list: %w", err)
	}
	open, isDelim := start.(json.Delim)
	if !isDelim || (open != '[' && open != '{') {
		return body, nil
	}

	accept := func(raw json.RawMessage) bool {
		var entry map[string]any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&entry) != nil || entry == nil {
			return true
		}
		return keep(entry)
	}

	var out bytes.Buffer
	out.WriteByte(byte(open))
	first := true
	for dec.More() {
		var key string
		if open == '{' {
			k, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("decoding provider list: %w", err)
			}
			key, _ = k.(string)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decoding provider list: %w", err)
		}
		if key == "user_info" || key == "server_info" {
			return body, nil // a login answer, not a list
		}
		if !accept(raw) {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		if open == '{' {
			name, _ := json.Marshal(key)
			out.Write(name)
			out.WriteByte(':')
		}
		out.Write(raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("decoding provider list: %w", err)
	}
	if open == '[' {
		out.WriteByte(']')
	} else {
		out.WriteByte('}')
	}
	return out.Bytes(), nil
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
