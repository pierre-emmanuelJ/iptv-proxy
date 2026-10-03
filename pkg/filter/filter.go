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
	"strings"
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

// Rules are compiled Patterns, possibly several sets of them: a channel is
// kept when every set keeps it. A nil *Rules keeps everything.
type Rules struct {
	group, channel               []*regexp.Regexp
	groupExclude, channelExclude []*regexp.Regexp
}

// New compiles the patterns. It returns nil when there is nothing to filter,
// and an error naming the option of an invalid expression.
func New(p Patterns) (*Rules, error) {
	r := &Rules{}
	for _, f := range []struct {
		option  string
		pattern string
		into    *[]*regexp.Regexp
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
		*f.into = append(*f.into, re)
	}
	if r.empty() {
		return nil, nil
	}
	return r, nil
}

// Combine returns the rules that keep a channel when all of rules keep it.
func Combine(rules ...*Rules) *Rules {
	all := &Rules{}
	for _, r := range rules {
		if r == nil {
			continue
		}
		all.group = append(all.group, r.group...)
		all.channel = append(all.channel, r.channel...)
		all.groupExclude = append(all.groupExclude, r.groupExclude...)
		all.channelExclude = append(all.channelExclude, r.channelExclude...)
	}
	if all.empty() {
		return nil
	}
	return all
}

func (r *Rules) empty() bool {
	return len(r.group)+len(r.channel)+len(r.groupExclude)+len(r.channelExclude) == 0
}

// String describes the rules: two rules with the same description keep the
// same channels.
func (r *Rules) String() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	for _, set := range []struct {
		name string
		res  []*regexp.Regexp
	}{{"g", r.group}, {"c", r.channel}, {"G", r.groupExclude}, {"C", r.channelExclude}} {
		for _, re := range set.res {
			fmt.Fprintf(&b, "%s%q", set.name, re.String())
		}
	}
	return b.String()
}

// Active tells whether anything is filtered.
func (r *Rules) Active() bool {
	return r != nil
}

// FiltersGroups tells whether a channel's group matters.
func (r *Rules) FiltersGroups() bool {
	return r != nil && len(r.group)+len(r.groupExclude) > 0
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

func keep(include, exclude []*regexp.Regexp, s string) bool {
	for _, re := range include {
		if !re.MatchString(s) {
			return false
		}
	}
	for _, re := range exclude {
		if re.MatchString(s) {
			return false
		}
	}
	return true
}
