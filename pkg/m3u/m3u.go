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

// Package m3u reads and writes extended M3U playlists without interpreting
// them: every line a provider wrote is kept as it is, and only the address of
// each track is meant to be replaced.
package m3u

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	header    = "#EXTM3U"
	extInf    = "#EXTINF"
	utf8BOM   = "\xEF\xBB\xBF"
	lineLimit = 16 << 20 // some providers inline logos as base64
)

// ErrNoHeader is returned for a document that is not an extended M3U playlist
// (typically the HTML or JSON error page of a provider).
var ErrNoHeader = errors.New("invalid m3u: expected an #EXTM3U header")

// Track is one entry of a playlist.
type Track struct {
	// ExtInf is the "#EXTINF:" line, verbatim. Empty when the entry had none.
	ExtInf string
	// Extra holds the other "#" lines of the entry (#EXTGRP, #EXTVLCOPT,
	// #KODIPROP...), verbatim and in order.
	Extra []string
	// URI is the address of the track.
	URI string
}

// Name is the display name of the track: what follows the attributes.
func (t Track) Name() string {
	quoted := false
	for i, r := range t.ExtInf {
		switch r {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				return strings.TrimSpace(t.ExtInf[i+1:])
			}
		}
	}
	return ""
}

// Playlist is an extended M3U playlist.
type Playlist struct {
	// Header is the "#EXTM3U" line, verbatim: it may carry the guide address
	// (url-tvg, x-tvg-url).
	Header string
	Tracks []Track
}

// Parse reads a playlist.
func Parse(r io.Reader) (*Playlist, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), lineLimit)

	playlist := &Playlist{}
	var pending *Track
	first := true

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			line = strings.TrimSpace(strings.TrimPrefix(line, utf8BOM))
		}
		if line == "" {
			continue
		}
		if first {
			first = false
			if !strings.HasPrefix(line, header) {
				return nil, ErrNoHeader
			}
			playlist.Header = line
			continue
		}

		switch {
		case strings.HasPrefix(line, extInf):
			pending = &Track{ExtInf: line}
		case strings.HasPrefix(line, "#"):
			// A directive before any #EXTINF belongs to no track.
			if pending != nil {
				pending.Extra = append(pending.Extra, line)
			}
		default:
			if pending == nil {
				pending = &Track{}
			}
			pending.URI = line
			playlist.Tracks = append(playlist.Tracks, *pending)
			pending = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading m3u: %w", err)
	}
	if first {
		return nil, ErrNoHeader
	}

	return playlist, nil
}

// WriteTo writes the playlist.
func (p *Playlist) WriteTo(w io.Writer) (int64, error) {
	bw := bufio.NewWriter(w)
	var n int64
	write := func(line string) {
		c, _ := bw.WriteString(line) // the error is reported by Flush
		_ = bw.WriteByte('\n')
		n += int64(c) + 1
	}

	h := p.Header
	if h == "" {
		h = header
	}
	write(h)
	for _, t := range p.Tracks {
		if t.ExtInf != "" {
			write(t.ExtInf)
		}
		for _, extra := range t.Extra {
			write(extra)
		}
		write(t.URI)
	}

	return n, bw.Flush()
}

// ExtInfLine builds an "#EXTINF" line for a live track from its attributes,
// skipping the empty ones. Attributes are written in the given order.
func ExtInfLine(name string, attrs ...[2]string) string {
	var b strings.Builder
	b.WriteString(extInf + ":-1")
	for _, a := range attrs {
		if a[1] == "" {
			continue
		}
		fmt.Fprintf(&b, " %s=\"%s\"", a[0], strings.ReplaceAll(a[1], "\"", "'"))
	}
	b.WriteString("," + name)
	return b.String()
}
