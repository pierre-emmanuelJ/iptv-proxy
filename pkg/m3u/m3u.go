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
	"regexp"
	"strings"
)

const (
	header    = "#EXTM3U"
	extInf    = "#EXTINF"
	extGrp    = "#EXTGRP:"
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

// Group is the group of the track: its group-title attribute, else its
// #EXTGRP line.
func (t Track) Group() string {
	if group, ok := Attribute(t.ExtInf, "group-title"); ok {
		return group
	}
	for _, extra := range t.Extra {
		if strings.HasPrefix(extra, extGrp) {
			return strings.TrimSpace(strings.TrimPrefix(extra, extGrp))
		}
	}
	return ""
}

// attribute is a name="value" attribute of an #EXTM3U or #EXTINF line.
var attribute = regexp.MustCompile(`([A-Za-z0-9_.:-]+)="([^"]*)"`)

// attributes returns where the attributes of a line may be: the whole
// #EXTM3U line, and an #EXTINF line up to its display name.
func attributes(line string) string {
	if !strings.HasPrefix(line, extInf) {
		return line
	}
	quoted := false
	for i, r := range line {
		switch r {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				return line[:i]
			}
		}
	}
	return line
}

// Attribute returns the value of an attribute of an #EXTM3U or #EXTINF line.
// Names are compared regardless of case, as players do: providers write
// "tvg-ID" as well as "tvg-id".
func Attribute(line, name string) (string, bool) {
	for _, m := range attribute.FindAllStringSubmatch(attributes(line), -1) {
		if strings.EqualFold(m[1], name) {
			return m[2], true
		}
	}
	return "", false
}

// EditAttributes calls edit for each attribute of an #EXTM3U or #EXTINF line
// and returns the line with the values it gives. An attribute edit does not
// keep is removed. The rest of the line is kept as it is.
func EditAttributes(line string, edit func(name, value string) (string, bool)) string {
	region := attributes(line)
	matches := attribute.FindAllStringSubmatchIndex(region, -1)
	if len(matches) == 0 {
		return line
	}

	var b strings.Builder
	last := 0
	for _, m := range matches {
		name, value := region[m[2]:m[3]], region[m[4]:m[5]]
		newValue, keep := edit(name, value)
		switch {
		case !keep:
			// the attribute goes with the blank before it
			b.WriteString(strings.TrimRight(region[last:m[0]], " \t"))
		case newValue != value:
			b.WriteString(region[last:m[4]])
			b.WriteString(strings.ReplaceAll(newValue, `"`, "'"))
			b.WriteString(region[m[5]:m[1]])
		default:
			b.WriteString(region[last:m[1]])
		}
		last = m[1]
	}
	b.WriteString(line[last:])
	return b.String()
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
