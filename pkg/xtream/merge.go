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
	"bytes"
	"encoding/json"
	"fmt"
)

// EachEntry calls do with each object of a provider list (categories,
// streams), decoded and as the provider wrote it, in the list's order. A list
// written as an object keyed by index is read in the order of its keys, as
// DecodeList does. "null", "false" and an empty object are empty lists.
func EachEntry(body []byte, do func(entry map[string]any, raw []byte) error) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	start, err := dec.Token()
	if err != nil {
		return fmt.Errorf("decoding provider list: %w", err)
	}
	open, isDelim := start.(json.Delim)
	if !isDelim || (open != '[' && open != '{') {
		if start == nil || start == false {
			return nil
		}
		return fmt.Errorf("decoding provider list: not a list")
	}

	each := func(raw []byte) error {
		var entry map[string]any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&entry) != nil || entry == nil {
			return nil // not an object
		}
		return do(entry, raw)
	}
	if open == '[' {
		// A list is read as it comes: it may be a catalogue of a
		// hundred thousand movies.
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return fmt.Errorf("decoding provider list: %w", err)
			}
			if err := each(raw); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("decoding provider list: %w", err)
		}
		return nil
	}

	type keyed struct {
		key string
		raw json.RawMessage
	}
	var entries []keyed
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return fmt.Errorf("decoding provider list: %w", err)
		}
		key, _ := k.(string)
		if key == "user_info" || key == "server_info" {
			return fmt.Errorf("the provider answered with its login page instead of a list")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("decoding provider list: %w", err)
		}
		entries = append(entries, keyed{key, raw})
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("decoding provider list: %w", err)
	}
	keys := make([]string, len(entries))
	byKey := make(map[string]json.RawMessage, len(entries))
	for i, e := range entries {
		keys[i] = e.key
		byKey[e.key] = e.raw
	}
	sortNumeric(keys)
	for _, k := range keys {
		if err := each(byKey[k]); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceFields returns an object with the values of some of its fields
// replaced: each key of values present in the object gets the JSON encoding
// of its value. Everything else is kept byte for byte.
func ReplaceFields(object []byte, values map[string]any) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(object))
	if start, err := dec.Token(); err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("not a JSON object")
	}
	var out bytes.Buffer
	done := 0 // bytes of object already written
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := k.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		end := int(dec.InputOffset())
		value, replace := values[key]
		if !replace {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out.Write(object[done : end-len(raw)])
		out.Write(encoded)
		done = end
	}
	out.Write(object[done:])
	return out.Bytes(), nil
}

// SameType returns text as the provider wrote the value it replaces: a JSON
// number when it was one and text is one too, else a string.
func SameType(was any, text string) any {
	if _, isNumber := was.(json.Number); isNumber {
		if _, err := json.Number(text).Int64(); err == nil {
			return json.Number(text)
		}
	}
	return text
}
