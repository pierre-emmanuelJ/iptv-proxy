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

// Package hls rewrites the addresses of an HLS playlist, whatever the
// provider: every address a player would fetch (variant playlists, segments,
// keys, initialization sections, alternate renditions) is resolved against
// the playlist's own address and handed to a function that returns what the
// player should ask instead.
package hls

import (
	"bytes"
	"net/url"
	"strings"
)

const header = "#EXTM3U"

// IsPlaylist tells whether a response starts like an HLS playlist. A byte
// order mark and leading blanks are allowed, as players allow them.
func IsPlaylist(start []byte) bool {
	start = bytes.TrimPrefix(start, []byte("\xEF\xBB\xBF"))
	start = bytes.TrimLeft(start, " \t\r\n")
	return bytes.HasPrefix(start, []byte(header))
}

// Rewrite returns the playlist with each address replaced by wrap's answer
// for it. base is the address the playlist was fetched from (after
// redirects): relative addresses are resolved against it. An address wrap
// has nothing to say about (it returns "") is left as it is, and so is one
// that is not http(s): a "data:" key, a "skd:" DRM address.
//
// Everything else is kept byte for byte, line endings included.
func Rewrite(playlist []byte, base *url.URL, wrap func(absolute *url.URL) string) []byte {
	replace := func(ref string) (string, bool) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return "", false
		}
		u, err := base.Parse(ref)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", false
		}
		if out := wrap(u); out != "" {
			return out, true
		}
		return "", false
	}

	var out bytes.Buffer
	out.Grow(len(playlist) + len(playlist)/2)

	for len(playlist) > 0 {
		line, ending := playlist, []byte(nil)
		if i := bytes.IndexByte(playlist, '\n'); i >= 0 {
			line, ending, playlist = playlist[:i], playlist[i:i+1], playlist[i+1:]
		} else {
			playlist = nil
		}
		if bytes.HasSuffix(line, []byte("\r")) {
			line, ending = line[:len(line)-1], append([]byte("\r"), ending...)
		}

		switch text := string(line); {
		case strings.TrimSpace(text) == "":
			out.Write(line)
		case strings.HasPrefix(strings.TrimSpace(text), "#"):
			out.WriteString(rewriteTag(text, replace))
		default:
			if ref, ok := replace(text); ok {
				out.WriteString(ref)
			} else {
				out.Write(line)
			}
		}
		out.Write(ending)
	}

	return out.Bytes()
}

// rewriteTag replaces the URI="..." attributes of a tag line (EXT-X-KEY,
// EXT-X-MAP, EXT-X-MEDIA, EXT-X-I-FRAME-STREAM-INF, EXT-X-PART...).
func rewriteTag(line string, replace func(string) (string, bool)) string {
	const attr = `URI="`

	var out strings.Builder
	rest := line
	for {
		i := indexAttribute(rest, attr)
		if i < 0 {
			break
		}
		start := i + len(attr)
		end := strings.IndexByte(rest[start:], '"')
		if end < 0 {
			break
		}
		end += start

		out.WriteString(rest[:start])
		if ref, ok := replace(rest[start:end]); ok {
			out.WriteString(ref)
		} else {
			out.WriteString(rest[start:end])
		}
		rest = rest[end:]
	}
	out.WriteString(rest)
	return out.String()
}

// indexAttribute finds an attribute by its exact name: `URI="` and not the
// end of another name such as `X-ASSET-URI="`.
func indexAttribute(s, attr string) int {
	from := 0
	for {
		i := strings.Index(s[from:], attr)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 || s[i-1] == ':' || s[i-1] == ',' || s[i-1] == ' ' {
			return i
		}
		from = i + len(attr)
	}
}
