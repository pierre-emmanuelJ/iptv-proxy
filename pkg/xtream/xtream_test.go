package xtream

import (
	"encoding/json"
	"strings"
	"testing"
)

var (
	provider = Account{BaseURL: "http://provider.example:8080", User: "xuser", Password: "x pass"}
	proxy    = Account{BaseURL: "https://proxy.example:443/tv", User: "me", Password: "secret"}
)

func TestAPIURL(t *testing.T) {
	got := provider.APIURL("player_api.php", map[string][]string{
		"action":   {"get_live_streams"},
		"username": {"someone-else"},
		"password": {"nope"},
	})
	want := "http://provider.example:8080/player_api.php?action=get_live_streams&password=x+pass&username=xuser"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestStreamURL(t *testing.T) {
	if got, want := provider.StreamURL("movie/", "12.mkv"), "http://provider.example:8080/movie/xuser/x%20pass/12.mkv"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string][2]string{
		"stream address, escaped slashes": {
			`{"direct_source":"http:\/\/provider.example:8080\/movie\/xuser\/x%20pass\/12.mkv"}`,
			`{"direct_source":"https:\/\/proxy.example:443\/tv\/movie\/me\/secret\/12.mkv"}`,
		},
		"live short form, plain slashes": {
			`{"url":"http://provider.example:8080/xuser/x%20pass/7.ts"}`,
			`{"url":"https://proxy.example:443/tv/me/secret/7.ts"}`,
		},
		"credentials on another host": {
			`{"url":"http://cdn.other.example/hls/xuser/x%20pass/7.m3u8"}`,
			`{"url":"http://cdn.other.example/hls/me/secret/7.m3u8"}`,
		},
		"credentials in a query": {
			`{"epg":"http://provider.example:8080/xmltv.php?username=xuser&password=x+pass"}`,
			`{"epg":"http://provider.example:8080/xmltv.php?username=me&password=secret"}`,
		},
		"logos stay on the provider": {
			`{"stream_icon":"http:\/\/provider.example:8080\/images\/one.png","name":"xuser"}`,
			`{"stream_icon":"http:\/\/provider.example:8080\/images\/one.png","name":"xuser"}`,
		},
	}
	for name, c := range cases {
		if got := string(Sanitize([]byte(c[0]), provider, proxy)); got != c[1] {
			t.Errorf("%s:\n got  %s\n want %s", name, got, c[1])
		}
	}
}

// Parameters are not always next to each other, nor in that order.
func TestSanitizeQueryParametersInAnyOrder(t *testing.T) {
	in := `{"url":"http://provider.example:8080/player_api.php?action=get_short_epg&limit=2&password=x+pass&stream_id=1&username=xuser"}`
	want := `{"url":"http://provider.example:8080/player_api.php?action=get_short_epg&limit=2&password=secret&stream_id=1&username=me"}`
	if got := string(Sanitize([]byte(in), provider, proxy)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestScrub(t *testing.T) {
	amp := Account{BaseURL: provider.BaseURL, User: "x&user", Password: `p<a>s"s w&d`}
	for name, page := range map[string]string{
		"address repeated":      `<html>404 Not Found: /player_api.php?action=x&password=p%3Ca%3Es%22s+w%26d&stream_id=1&username=x%26user</html>`,
		"address, html escaped": `<p>/player_api.php?action=x&amp;password=p%3Ca%3Es%22s+w%26d&amp;username=x%26user</p>`,
		"path":                  `No such file: /live/x&user/p%3Ca%3Es%22s%20w&d/1.ts`,
		"plain":                 `Account x&user (p<a>s"s w&d) is not allowed`,
		"plain, html escaped":   `Account x&amp;user (p&lt;a&gt;s&#34;s w&amp;d) is not allowed`,
	} {
		got := string(Scrub([]byte(page), amp, proxy))
		if Leaks(got, amp) || strings.Contains(got, "x&user") || strings.Contains(got, "x%26user") || strings.Contains(got, "x&amp;user") {
			t.Errorf("%s: still there in %s", name, got)
		}
		if !strings.Contains(got, "secret") || !strings.Contains(got, "me") {
			t.Errorf("%s: the proxy's credentials should take their place: %s", name, got)
		}
	}

	if got := string(Scrub([]byte("nothing to hide"), amp, proxy)); got != "nothing to hide" {
		t.Errorf("got %q", got)
	}
	if got := string(Scrub([]byte("xuser"), Account{}, proxy)); got != "xuser" {
		t.Errorf("an account with no credentials changes nothing, got %q", got)
	}
}

func TestLeaks(t *testing.T) {
	for text, want := range map[string]bool{
		"password=x+pass": true, "/x%20pass/": true, "x pass": true, "xuser only": false, "": false,
	} {
		if got := Leaks(text, provider); got != want {
			t.Errorf("Leaks(%q) = %v, want %v", text, got, want)
		}
	}
	if Leaks("anything", Account{}) {
		t.Error("an empty password leaks nowhere")
	}
}

func TestSanitizeNeverLeavesThePassword(t *testing.T) {
	body := `[{"a":"http:\/\/provider.example:8080\/series\/xuser\/x%20pass\/1.mp4"},{"b":"/xuser/x pass/2"},{"c":"?username=xuser&amp;password=x+pass"}]`
	got := string(Sanitize([]byte(body), provider, proxy))
	for _, leak := range []string{"x pass", "x%20pass", "x+pass", "xuser"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q is still in %s", leak, got)
		}
	}
}

// Real-world shapes: every field quoted or not depending on the provider,
// and fields the proxy has never heard of.
func TestRewriteLogin(t *testing.T) {
	info := ProxyInfo{Account: proxy, Hostname: "proxy.example", Port: 443, Protocol: "https"}
	for name, body := range map[string]string{
		"quoted numbers": `{"user_info":{"username":"xuser","password":"x pass","auth":1,"status":"Active","exp_date":"1767225600","is_trial":"0","active_cons":"0","created_at":"1700000000","max_connections":"2","allowed_output_formats":["m3u8","ts"]},"server_info":{"url":"provider.example","port":"8080","https_port":"8443","server_protocol":"http","rtmp_port":"8880","timezone":"Europe\/Paris","timestamp_now":1759400000,"time_now":"2025-10-02 12:00:00"}}`,
		"bare numbers, null expiry, extra fields": `{"user_info":{"username":"xuser","password":"x pass","auth":1,"exp_date":null,"max_connections":2,"active_cons":0,"is_trial":0,"created_at":1700000000,"allowed_output_formats":"ts","brand_new_field":{"x":[1,2]}},"server_info":{"url":"provider.example","port":8080,"https_port":8443,"rtmp_port":"","xui":true,"version":"1.5.12","revision":2,"process":true}}`,
	} {
		out, ok := RewriteLogin([]byte(body), info)
		if !ok {
			t.Fatalf("%s: not rewritten", name)
		}
		var got, original struct {
			User   map[string]any `json:"user_info"`
			Server map[string]any `json:"server_info"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_ = json.Unmarshal([]byte(body), &original)

		if got.User["username"] != "me" || got.User["password"] != "secret" {
			t.Errorf("%s: credentials = %v / %v", name, got.User["username"], got.User["password"])
		}
		for field, want := range map[string]any{"url": "https://proxy.example", "server_protocol": "https"} {
			if got.Server[field] != want {
				t.Errorf("%s: server_info.%s = %v, want %v", name, field, got.Server[field], want)
			}
		}
		// a port keeps the JSON type the provider gave it
		for _, field := range []string{"port", "https_port", "rtmp_port"} {
			var want any = "443"
			if _, wasNumber := original.Server[field].(float64); wasNumber {
				want = float64(443)
			}
			if got.Server[field] != want {
				t.Errorf("%s: server_info.%s = %#v, want %#v", name, field, got.Server[field], want)
			}
		}
		for field, want := range original.User {
			if field == "username" || field == "password" {
				continue
			}
			if a, _ := json.Marshal(got.User[field]); string(a) != mustJSON(want) {
				t.Errorf("%s: user_info.%s changed: %s, was %s", name, field, a, mustJSON(want))
			}
		}
		if strings.Contains(string(out), "xuser") || strings.Contains(string(out), "x pass") {
			t.Errorf("%s: provider credentials are still in %s", name, out)
		}
	}
}

func TestRewriteLoginKeepsNumbersAsWritten(t *testing.T) {
	out, _ := RewriteLogin([]byte(`{"user_info":{"exp_date":1767225600123456789,"ratio":1.50}}`), ProxyInfo{})
	if !strings.Contains(string(out), "1767225600123456789") || !strings.Contains(string(out), "1.50") {
		t.Errorf("numbers were reformatted: %s", out)
	}
}

func TestRewriteLoginLeavesOtherAnswersAlone(t *testing.T) {
	for _, body := range []string{"", "<html>Blocked</html>", "[]", "null", `"text"`} {
		out, ok := RewriteLogin([]byte(body), ProxyInfo{})
		if ok || string(out) != body {
			t.Errorf("%q: got %q, ok=%v", body, out, ok)
		}
	}
}

func TestText(t *testing.T) {
	var values []any
	dec := json.NewDecoder(strings.NewReader(`[12, "12", 12.0, "abc", null, true, 1.5, 123456789012]`))
	dec.UseNumber()
	if err := dec.Decode(&values); err != nil {
		t.Fatal(err)
	}
	want := []string{"12", "12", "12", "abc", "", "true", "1.5", "123456789012"}
	for i, v := range values {
		if got := Text(v); got != want[i] {
			t.Errorf("Text(%v) = %q, want %q", v, got, want[i])
		}
	}
}

func TestDecodeList(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		ids  string
	}{
		"array":                 {`[{"stream_id":1},{"stream_id":"2"}]`, "1,2"},
		"object keyed by index": {`{"10":{"stream_id":3},"2":{"stream_id":2},"1":{"stream_id":1}}`, "1,2,3"},
		"empty array":           {`[]`, ""},
		"empty object":          {`{}`, ""},
		"null":                  {`null`, ""},
		"false":                 {`false`, ""},
		"junk entries":          {`[{"stream_id":1}, "x", null, 3]`, "1"},
	} {
		items, err := DecodeList([]byte(c.body))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var ids []string
		for _, item := range items {
			ids = append(ids, Text(item["stream_id"]))
		}
		if got := strings.Join(ids, ","); got != c.ids {
			t.Errorf("%s: ids = %q, want %q", name, got, c.ids)
		}
	}

	if _, err := DecodeList([]byte(`{"user_info":{"auth":0}}`)); err == nil {
		t.Error("a login answer is not a list")
	}
	if _, err := DecodeList([]byte(`<html>`)); err == nil {
		t.Error("HTML is not a list")
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
