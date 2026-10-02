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

// Package filter decides which channels a client gets to see, from regular
// expressions on their group and on their name.
package filter

import (
	"fmt"
	"regexp"
)

// Patterns are the regular expressions a channel is checked against. An
// empty one does not filter.
type Patterns struct {
	// Group and Channel keep what they match.
	Group, Channel string
	// GroupExclude and ChannelExclude drop what they match, even when it was
	// kept above.
	GroupExclude, ChannelExclude string
}

// Rules are compiled Patterns. A nil *Rules keeps everything.
type Rules struct {
	group, channel               *regexp.Regexp
	groupExclude, channelExclude *regexp.Regexp
}

// New compiles the patterns. It returns nil when there is nothing to filter,
// and an error naming the option of an invalid expression.
func New(p Patterns) (*Rules, error) {
	r := &Rules{}
	for _, f := range []struct {
		option  string
		pattern string
		into    **regexp.Regexp
	}{
		{"group-regex", p.Group, &r.group},
		{"channel-regex", p.Channel, &r.channel},
		{"group-exclude-regex", p.GroupExclude, &r.groupExclude},
		{"channel-exclude-regex", p.ChannelExclude, &r.channelExclude},
	} {
		if f.pattern == "" {
			continue
		}
		re, err := regexp.Compile(f.pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid --%s: %w", f.option, err)
		}
		*f.into = re
	}
	if r.group == nil && r.channel == nil && r.groupExclude == nil && r.channelExclude == nil {
		return nil, nil
	}
	return r, nil
}

// Active tells whether anything is filtered.
func (r *Rules) Active() bool {
	return r != nil
}

// FiltersGroups tells whether a channel's group matters.
func (r *Rules) FiltersGroups() bool {
	return r != nil && (r.group != nil || r.groupExclude != nil)
}

// Group tells whether channels of this group are kept, whatever their name.
func (r *Rules) Group(name string) bool {
	if r == nil {
		return true
	}
	return keep(r.group, r.groupExclude, name)
}

// Keep tells whether a channel is kept.
func (r *Rules) Keep(group, channel string) bool {
	if r == nil {
		return true
	}
	return keep(r.group, r.groupExclude, group) && keep(r.channel, r.channelExclude, channel)
}

func keep(include, exclude *regexp.Regexp, s string) bool {
	if include != nil && !include.MatchString(s) {
		return false
	}
	return exclude == nil || !exclude.MatchString(s)
}
