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

package xtream

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEachEntry(t *testing.T) {
	for name, body := range map[string]string{
		"a list":             `[{"id":1}, 7, {"id":"2"}]`,
		"an object by index": `{"10":{"id":"2"},"9":{"id":1}}`,
	} {
		var got []string
		err := EachEntry([]byte(body), func(entry map[string]any, raw []byte) error {
			got = append(got, Text(entry["id"])+"="+string(raw))
			return nil
		})
		want := `1={"id":1},2={"id":"2"}`
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("%s: %v %v", name, err, got)
		}
	}
	for _, empty := range []string{"null", "false", "{}", "[]"} {
		calls := 0
		if err := EachEntry([]byte(empty), func(map[string]any, []byte) error { calls++; return nil }); err != nil || calls != 0 {
			t.Errorf("%s: %v, %d calls", empty, err, calls)
		}
	}
	for _, bad := range []string{"", "<html>", `"text"`, `{"user_info":{}}`, `[{"id":1}`, `{"1":}`} {
		if err := EachEntry([]byte(bad), func(map[string]any, []byte) error { return nil }); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	stop := errors.New("stop")
	if err := EachEntry([]byte(`[{},{}]`), func(map[string]any, []byte) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("err = %v", err)
	}
}

func TestReplaceFields(t *testing.T) {
	object := `{ "num" : 1, "stream_id": 12 ,"name":"A \"b\"","category_ids":[3,4],"nested":{"stream_id":5}}`
	got, err := ReplaceFields([]byte(object), map[string]any{
		"stream_id":    json.Number("100000012"),
		"category_ids": []any{json.Number("3"), json.Number("100000004")},
		"absent":       "x",
	})
	want := `{ "num" : 1, "stream_id": 100000012 ,"name":"A \"b\"","category_ids":[3,100000004],"nested":{"stream_id":5}}`
	if err != nil || string(got) != want {
		t.Errorf("err %v\n got %s\nwant %s", err, got, want)
	}
	for _, bad := range []string{"[1]", "", `{"a":}`} {
		if _, err := ReplaceFields([]byte(bad), map[string]any{"a": 1}); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestSameType(t *testing.T) {
	if v := SameType(json.Number("1"), "100000001"); v != json.Number("100000001") {
		t.Errorf("number: %#v", v)
	}
	if v := SameType("1", "100000001"); v != "100000001" {
		t.Errorf("string: %#v", v)
	}
	if v := SameType(json.Number("1"), "abc"); v != "abc" {
		t.Errorf("not a number: %#v", v)
	}
}

// Whatever a provider sends, the entries of its lists get their ids
// replaced into valid JSON, the rest untouched.
func FuzzMergeList(f *testing.F) {
	f.Add(`[{"stream_id":1,"name":"a","category_ids":[1,2]}]`)
	f.Add(`{"1":{"stream_id":"2","x":{"stream_id":3}}, "0":{}}`)
	f.Fuzz(func(t *testing.T, doc string) {
		_ = EachEntry([]byte(doc), func(entry map[string]any, raw []byte) error {
			out, err := ReplaceFields(raw, map[string]any{"stream_id": json.Number("100000001"), "category_ids": []any{"x"}})
			if err != nil {
				t.Fatalf("an entry of the list: %v\n%q", err, raw)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("not JSON any more: %v\n%q\n%q", err, raw, out)
			}
			if _, had := entry["stream_id"]; had && got["stream_id"] != float64(100000001) {
				t.Fatalf("stream_id not replaced:\n%q\n%q", raw, out)
			}
			return nil
		})
	})
}
