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

// Package xmltv filters an XMLTV guide by channel, as it streams: a guide
// can weigh hundreds of megabytes.
//
// The guide is not decoded and encoded again: everything is copied byte for
// byte, except the <channel> and <programme> elements of the channels left
// out.
package xmltv

import (
	"bufio"
	"bytes"
	"errors"
	"html"
	"io"
	"regexp"
)

// maxElement bounds one <channel> or <programme> element (an icon may be
// inlined).
const maxElement = 16 << 20

// ErrElementTooLarge is returned for an element larger than any real guide
// has.
var ErrElementTooLarge = errors.New("xmltv: element too large")

// elements are the elements filtered, with the attribute naming their
// channel.
var elements = map[string]*regexp.Regexp{
	"channel":   regexp.MustCompile(`\sid\s*=\s*(?:"([^"]*)"|'([^']*)')`),
	"programme": regexp.MustCompile(`\schannel\s*=\s*(?:"([^"]*)"|'([^']*)')`),
}

// iconSource finds the address of the images of an element: <icon src=...>.
var iconSource = regexp.MustCompile(`<icon\s[^>]*?\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// Option changes how a guide is copied.
type Option func(*copying)

type copying struct {
	icon func(address string) string
}

// Icons gives the images of the guide (channel logos, programme pictures)
// the addresses icon returns for theirs.
func Icons(icon func(address string) string) Option {
	return func(c *copying) { c.icon = icon }
}

// Filter copies the guide from src to dst without the channels keep
// refuses, nor their programmes.
func Filter(dst io.Writer, src io.Reader, keep func(channel string) bool) error {
	return Map(dst, src, func(channel string) []string {
		if keep(channel) {
			return []string{channel}
		}
		return nil
	})
}

// Map copies the guide from src to dst, giving each channel the ids ids
// returns for it: none leaves the channel and its programmes out, its own
// keeps it as it is, another renames it, several copy it under each. A
// renamed channel also gets its new id as its first display name: that is
// how media servers match a guide to the channel numbers of a tuner.
func Map(dst io.Writer, src io.Reader, ids func(channel string) []string, options ...Option) error {
	return copyGuide(dst, src, ids, nil, false, options)
}

// Merge copies the guide from src as Map does, and writes what more writes
// just before the guide's end tag: typically the channels of other guides,
// given by Elements.
func Merge(dst io.Writer, src io.Reader, ids func(channel string) []string, more func(w io.Writer) error, options ...Option) error {
	return copyGuide(dst, src, ids, more, false, options)
}

// Elements writes the <channel> and <programme> elements of the guide from
// src, as Map gives them their ids, one per line, and nothing else: the
// guide's own head and end are left out.
func Elements(dst io.Writer, src io.Reader, ids func(channel string) []string, options ...Option) error {
	return copyGuide(dst, src, ids, nil, true, options)
}

// copyGuide copies a guide, or only its elements, with more written before
// its end tag.
func copyGuide(dst io.Writer, src io.Reader, ids func(channel string) []string, more func(w io.Writer) error, elementsOnly bool, options []Option) error {
	var how copying
	for _, option := range options {
		option(&how)
	}
	r := bufio.NewReaderSize(src, 64<<10)
	w := bufio.NewWriterSize(dst, 64<<10)
	// After an element left out, the blanks that followed it go too: the
	// guide keeps its layout.
	skipBlank := false

	for {
		text, err := r.ReadSlice('<')
		if skipBlank {
			text = bytes.TrimLeft(text, " \t\r\n")
			skipBlank = len(text) == 0
		}
		if elementsOnly {
			text = nil // only elements are written
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			if _, err := w.Write(text); err != nil {
				return err
			}
			continue
		case errors.Is(err, io.EOF):
			if _, err := w.Write(text); err != nil {
				return err
			}
			return w.Flush()
		case err != nil:
			return err
		}
		skipBlank = false
		if len(text) > 0 {
			if _, err := w.Write(text[:len(text)-1]); err != nil {
				return err
			}
		}

		name := tagName(r)
		if more != nil && isEndOfGuide(r) {
			if err := more(w); err != nil {
				return err
			}
			more = nil
		}
		switch {
		case elementsOnly && opaque(r) != "":
			err = readThrough(r, opaque(r), func(...byte) error { return nil })
		case opaque(r) != "":
			err = copyThrough(w, r, "<", opaque(r))
		case elements[name] != nil:
			var element []byte
			var startTag int
			element, startTag, err = readElement(r, name)
			if err == nil && how.icon != nil {
				element = withIcons(element, how.icon)
			}
			if err == nil {
				to := ids(channelOf(element[:startTag], name))
				for i, id := range to {
					if i > 0 {
						err = w.WriteByte('\n')
					}
					if err == nil {
						err = writeAs(w, element, startTag, name, id)
					}
				}
				if elementsOnly && len(to) > 0 && err == nil {
					err = w.WriteByte('\n')
				}
				skipBlank = len(to) == 0
			}
		case elementsOnly:
			// a tag of the guide's own: left out with what follows it
		default:
			err = w.WriteByte('<')
		}
		if err != nil {
			return err
		}
	}
}

// writeAs writes an element under the channel id given.
func writeAs(w *bufio.Writer, element []byte, startTag int, name, id string) error {
	m := elements[name].FindSubmatchIndex(element[:startTag])
	if m == nil {
		_, err := w.Write(element) // no channel to rename
		return err
	}
	start, end := m[2], m[3]
	if start < 0 {
		start, end = m[4], m[5]
	}
	if html.UnescapeString(string(element[start:end])) == id {
		_, err := w.Write(element)
		return err
	}

	escaped := html.EscapeString(id)
	_, _ = w.Write(element[:start])
	_, _ = w.WriteString(escaped)
	if name != "channel" {
		_, err := w.Write(element[end:])
		return err
	}
	label := "<display-name>" + escaped + "</display-name>"
	if element[startTag-2] == '/' {
		// <channel id="x"/> becomes <channel id="1">label</channel>
		_, _ = w.Write(element[end : startTag-2])
		_, err := w.WriteString(">" + label + "</channel>")
		return err
	}
	// The id comes after the channel's names: Plex shows the first one.
	at := startTag
	if i := bytes.LastIndex(element[startTag:], []byte("</display-name>")); i >= 0 {
		at += i + len("</display-name>")
	}
	_, _ = w.Write(element[end:at])
	_, _ = w.WriteString(label)
	_, err := w.Write(element[at:])
	return err
}

// withIcons gives the images of an element the addresses icon returns.
func withIcons(element []byte, icon func(string) string) []byte {
	found := iconSource.FindAllSubmatchIndex(element, -1)
	if found == nil {
		return element
	}
	var out []byte
	done := 0
	for _, m := range found {
		start, end := m[2], m[3]
		if start < 0 {
			start, end = m[4], m[5]
		}
		address := html.UnescapeString(string(element[start:end]))
		if to := icon(address); to != address {
			out = append(out, element[done:start]...)
			out = append(out, html.EscapeString(to)...)
			done = end
		}
	}
	return append(out, element[done:]...)
}

// tagName returns the name of the filtered element the reader is in, without
// reading it, or "".
func tagName(r *bufio.Reader) string {
	ahead, _ := r.Peek(len("programme") + 1)
	for name := range elements {
		if len(ahead) > len(name) && string(ahead[:len(name)]) == name && isTagEnd(ahead[len(name)]) {
			return name
		}
	}
	return ""
}

// isEndOfGuide tells whether the reader, just after a "<", is at the
// guide's end tag.
func isEndOfGuide(r *bufio.Reader) bool {
	ahead, _ := r.Peek(len("/tv") + 1)
	return len(ahead) == len("/tv")+1 && string(ahead[:3]) == "/tv" && isTagEnd(ahead[3])
}

func isTagEnd(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '>' || b == '/'
}

// copyThrough writes prefix, then copies from r up to and including end.
func copyThrough(w *bufio.Writer, r *bufio.Reader, prefix, end string) error {
	if _, err := w.WriteString(prefix); err != nil {
		return err
	}
	return readThrough(r, end, func(b ...byte) error {
		_, err := w.Write(b)
		return err
	})
}

// readElement reads a whole element whose "<" was just read: its start tag,
// and its content up to its end tag unless it closes itself. It also
// returns the length of the start tag.
func readElement(r *bufio.Reader, name string) ([]byte, int, error) {
	element := []byte{'<'}
	add := func(b ...byte) error {
		if len(element)+len(b) > maxElement {
			return ErrElementTooLarge
		}
		element = append(element, b...)
		return nil
	}

	// the start tag, up to its ">" outside of a quoted value
	var quote byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, 0, noEOF(err)
		}
		if err := add(b); err != nil {
			return nil, 0, err
		}
		if quote != 0 {
			if b == quote {
				quote = 0
			}
			continue
		}
		if b == '"' || b == '\'' {
			quote = b
		}
		if b == '>' {
			break
		}
	}
	startTag := len(element)
	if element[startTag-2] == '/' {
		return element, startTag, nil
	}

	// the content, up to the end tag
	end := "/" + name
	for {
		chunk, err := r.ReadSlice('<')
		full := errors.Is(err, bufio.ErrBufferFull)
		if err != nil && !full {
			return nil, 0, noEOF(err)
		}
		if err := add(chunk...); err != nil {
			return nil, 0, err
		}
		if full {
			continue
		}
		// a description may hold markup in a CDATA section or a comment
		if skip := opaque(r); skip != "" {
			if err := readThrough(r, skip, add); err != nil {
				return nil, 0, err
			}
			continue
		}
		ahead, _ := r.Peek(len(end) + 1)
		if len(ahead) == len(end)+1 && string(ahead[:len(end)]) == end && isTagEnd(ahead[len(end)]) {
			rest, err := r.ReadSlice('>')
			if err != nil {
				return nil, 0, noEOF(err)
			}
			if err := add(rest...); err != nil {
				return nil, 0, err
			}
			return element, startTag, nil
		}
	}
}

// opaque returns the end of the CDATA section or comment the reader is at
// the start of, if any.
func opaque(r *bufio.Reader) string {
	ahead, _ := r.Peek(len("![CDATA["))
	switch {
	case bytes.HasPrefix(ahead, []byte("![CDATA[")):
		return "]]>"
	case bytes.HasPrefix(ahead, []byte("!--")):
		return "-->"
	}
	return ""
}

// readThrough reads up to and including end.
func readThrough(r *bufio.Reader, end string, add func(...byte) error) error {
	var tail []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return noEOF(err)
		}
		if err := add(b); err != nil {
			return err
		}
		tail = append(tail, b)
		if len(tail) > len(end) {
			tail = tail[1:]
		}
		if string(tail) == end {
			return nil
		}
	}
}

func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// channelOf reads the channel an element belongs to, from its start tag.
func channelOf(startTag []byte, name string) string {
	m := elements[name].FindSubmatch(startTag)
	if m == nil {
		return ""
	}
	value := m[1]
	if value == nil {
		value = m[2]
	}
	return html.UnescapeString(string(value))
}
