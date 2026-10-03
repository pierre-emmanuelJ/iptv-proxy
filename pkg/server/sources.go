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

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// Several sources (Xtream accounts, of one provider or several) are served
// as one catalogue. Each source keeps its ids in a range of its own: source
// n shows id as n × sourceIDs + id, and the first source keeps its ids, so
// that adding a source changes nothing for what clients already know.
// Categories of the same name are one. A live channel several sources have
// (the same guide id) is shown once, from the first of them; the others are
// its fallbacks, used when it fails or its source has no connection left.
// With a single source, nothing of this applies: answers are the
// provider's own.

const sourceIDs = 100_000_000

type source struct {
	index   int
	name    string
	account xtream.Account
	// max is the configured limit of streams at once; 0 asks the provider.
	max int

	mu     sync.Mutex
	limit  int // 0: no limit
	asked  bool
	opened int // streams open at the provider
}

// catalogue is one of the Xtream catalogues: its categories and its list.
type catalogue struct {
	categories, list, id string
	live                 bool
}

var catalogues = []catalogue{
	{"get_live_categories", "get_live_streams", "stream_id", true},
	{"get_vod_categories", "get_vod_streams", "stream_id", false},
	{"get_series_categories", "get_series", "series_id", false},
}

// routedActions are the API actions about one entry, by the parameter that
// names it: they are asked to the entry's source.
var routedActions = map[string]string{
	"get_vod_info":          "vod_id",
	"get_series_info":       "series_id",
	"get_short_epg":         "stream_id",
	"get_simple_data_table": "stream_id",
}

// merging is what the proxy keeps of the merged catalogues.
type merging struct {
	mu sync.Mutex
	// categories map a source's category to the one shown, by catalogue.
	categories map[string]shownCategories
	// fallbacks are, for each live channel shown, the same channel at the
	// other sources.
	fallbacks   map[string][]sourceEntry
	fallbacksAt time.Time
}

type shownCategories struct {
	ids map[string]string // by sourceKey
	at  time.Time
}

// sourceEntry is an entry of a source, by its id there.
type sourceEntry struct {
	src *source
	id  string
}

// candidate is a source's address of a stream.
type candidate struct {
	src *source
	url string
}

func sourceKey(src int, id string) string {
	return strconv.Itoa(src) + "\x00" + id
}

// setupSources builds the sources: the account of the Xtream options, then
// those of the configuration file.
func (c *Config) setupSources() error {
	if len(c.Sources) == 0 {
		return nil
	}
	switch {
	case c.XtreamPassthrough:
		return errors.New("--xtream-passthrough has no sources of its own: leave out the sources of the configuration file")
	case c.servesM3U():
		return errors.New("sources are Xtream accounts: they do not go with an M3U playlist (--m3u-url)")
	}
	if c.XtreamBaseURL != "" {
		c.sources = append(c.sources, &source{name: "xtream", account: c.providerAccount()})
	}
	for i, s := range c.Sources {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("source %d", i+1)
		}
		if s.XtreamBaseURL == "" || s.XtreamUser == "" || s.XtreamPassword == "" {
			return fmt.Errorf("%s: a source needs xtream-base-url, xtream-user and xtream-password", name)
		}
		c.sources = append(c.sources, &source{
			name:    name,
			account: xtream.Account{BaseURL: s.XtreamBaseURL, User: s.XtreamUser, Password: s.XtreamPassword},
			max:     s.MaxConnections,
		})
	}
	for i, s := range c.sources {
		s.index = i
	}
	// The first source is the proxy's Xtream account.
	first := c.sources[0].account
	c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword = first.BaseURL, config.CredentialString(first.User), config.CredentialString(first.Password)
	c.merge.categories = map[string]shownCategories{}
	return nil
}

// merged tells whether several sources are served as one.
func (c *Config) merged() bool {
	return len(c.sources) > 1
}

// shownID is the id a source's entry is shown under.
func shownID(src int, id string) string {
	if src == 0 {
		return id
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 0 || n >= sourceIDs {
		return id // not an id the proxy can place: left as it is
	}
	return strconv.FormatInt(int64(src)*sourceIDs+n, 10)
}

// sourceOf returns the source of an id shown, and its id there.
func (c *Config) sourceOf(id string) (*source, string) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < sourceIDs || n/sourceIDs >= int64(len(c.sources)) {
		return c.sources[0], id
	}
	return c.sources[n/sourceIDs], strconv.FormatInt(n%sourceIDs, 10)
}

// fromEach asks every source the same API action, at once. A source that
// fails is left out, and the answer is then partial; when they all fail, it
// is an error. Answers are cleaned of each source's credentials for the
// request's user.
func (c *Config) fromEach(ctx *gin.Context, params url.Values) (bodies [][]byte, partial bool, err error) {
	bodies = make([][]byte, len(c.sources))
	errs := make([]error, len(c.sources))
	var wg sync.WaitGroup
	for i, src := range c.sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, body, err := c.accountGet(ctx, src.account, "player_api.php", params)
			switch {
			case err != nil:
				errs[i] = err
			case status != http.StatusOK:
				errs[i] = fmt.Errorf("HTTP %d", status)
			case !json.Valid(body):
				errs[i] = errors.New("an answer that is not JSON")
			default:
				bodies[i] = body
			}
		}()
	}
	wg.Wait()

	u, hasUser := ctx.Get(userKey)
	failed := 0
	for i, src := range c.sources {
		if errs[i] != nil {
			failed++
			log.Printf("[iptv-proxy] %s: %s failed (%v)", params.Get("action"), src.name, errs[i])
			continue
		}
		if hasUser {
			bodies[i] = xtream.Sanitize(bodies[i], src.account, c.proxyAccount(u.(*proxyUser)))
		}
	}
	if failed == len(c.sources) {
		return nil, true, fmt.Errorf("%s: every source failed", params.Get("action"))
	}
	return bodies, failed > 0, nil
}

// mergedCategories returns the categories of a catalogue at every source,
// those of the same name as one, and how each source's category is shown.
func (c *Config) mergedCategories(ctx *gin.Context, cat catalogue) ([]byte, map[string]string, bool, error) {
	bodies, partial, err := c.fromEach(ctx, url.Values{"action": {cat.categories}})
	if err != nil {
		return nil, nil, true, err
	}
	type firstOf struct {
		src   int
		shown string
	}
	byName := map[string]firstOf{}
	ids := map[string]string{}
	var out listWriter
	for i, body := range bodies {
		if body == nil {
			continue
		}
		err := xtream.EachEntry(body, func(entry map[string]any, raw []byte) error {
			id, name := xtream.Text(entry["category_id"]), xtream.Text(entry["category_name"])
			if first, ok := byName[name]; ok && first.src != i {
				ids[sourceKey(i, id)] = first.shown
				return nil
			}
			shown := shownID(i, id)
			if _, ok := byName[name]; !ok {
				byName[name] = firstOf{i, shown}
			}
			ids[sourceKey(i, id)] = shown
			if shown != id {
				var err error
				if raw, err = xtream.ReplaceFields(raw, map[string]any{"category_id": xtream.SameType(entry["category_id"], shown)}); err != nil {
					return err
				}
			}
			out.add(raw)
			return nil
		})
		if err != nil {
			log.Printf("[iptv-proxy] %s: %s: %v", cat.categories, c.sources[i].name, err)
			partial = true
		}
	}
	if !partial {
		c.merge.mu.Lock()
		c.merge.categories[cat.categories] = shownCategories{ids: ids, at: time.Now()}
		c.merge.mu.Unlock()
	}
	return out.list(), ids, partial, nil
}

// categoryIDs returns how the categories of each source are shown, asked
// again once older than the playlist cache.
func (c *Config) categoryIDs(ctx *gin.Context, cat catalogue) (map[string]string, error) {
	c.merge.mu.Lock()
	known := c.merge.categories[cat.categories]
	c.merge.mu.Unlock()
	if known.ids != nil && time.Since(known.at) < time.Duration(c.M3UCacheExpiration)*time.Hour {
		return known.ids, nil
	}
	_, ids, _, err := c.mergedCategories(ctx, cat)
	return ids, err
}

// mergedList returns the list of a catalogue at every source, its ids and
// categories as shown. A live channel a source before had already (the same
// guide id) is left out, and becomes that channel's fallback. The provider's
// category_id parameter is applied to the merged list.
func (c *Config) mergedList(ctx *gin.Context, cat catalogue, params url.Values) ([]byte, bool, error) {
	categories, err := c.categoryIDs(ctx, cat)
	if err != nil {
		return nil, true, err
	}
	asked := url.Values{}
	for k, v := range params {
		if k != "category_id" {
			asked[k] = v
		}
	}
	asked.Set("action", cat.list)
	bodies, partial, err := c.fromEach(ctx, asked)
	if err != nil {
		return nil, true, err
	}
	wanted := params.Get("category_id")

	type firstOf struct {
		src   int
		shown []string
	}
	byGuide := map[string]*firstOf{}
	fallbacks := map[string][]sourceEntry{}
	showCategory := func(src int, id string) string {
		if shown, ok := categories[sourceKey(src, id)]; ok {
			return shown
		}
		return shownID(src, id)
	}
	var out listWriter
	for i, body := range bodies {
		if body == nil {
			continue
		}
		err := xtream.EachEntry(body, func(entry map[string]any, raw []byte) error {
			id := xtream.Text(entry[cat.id])
			shown := shownID(i, id)
			if guide := xtream.Text(entry["epg_channel_id"]); cat.live && guide != "" {
				first := byGuide[guide]
				switch {
				case first == nil:
					byGuide[guide] = &firstOf{src: i, shown: []string{shown}}
				case first.src == i:
					first.shown = append(first.shown, shown) // the same channel twice at one source (HD, FHD)
				default:
					for _, primary := range first.shown {
						fallbacks[primary] = append(fallbacks[primary], sourceEntry{c.sources[i], id})
					}
					return nil
				}
			}

			values := map[string]any{}
			if shown != id {
				values[cat.id] = xtream.SameType(entry[cat.id], shown)
			}
			category := xtream.Text(entry["category_id"])
			shownCategory := showCategory(i, category)
			if shownCategory != category {
				values["category_id"] = xtream.SameType(entry["category_id"], shownCategory)
			}
			inWanted := wanted == "" || shownCategory == wanted
			if more, ok := entry["category_ids"].([]any); ok {
				shownMore := make([]any, len(more))
				for j, other := range more {
					shownOther := showCategory(i, xtream.Text(other))
					shownMore[j] = xtream.SameType(other, shownOther)
					inWanted = inWanted || shownOther == wanted
				}
				values["category_ids"] = shownMore
			}
			if !inWanted {
				return nil
			}
			if i > 0 || len(values) > 0 {
				var err error
				if raw, err = xtream.ReplaceFields(raw, values); err != nil {
					return err
				}
			}
			out.add(raw)
			return nil
		})
		if err != nil {
			log.Printf("[iptv-proxy] %s: %s: %v", cat.list, c.sources[i].name, err)
			partial = true
		}
	}
	if cat.live && !partial {
		c.merge.mu.Lock()
		c.merge.fallbacks, c.merge.fallbacksAt = fallbacks, time.Now()
		c.merge.mu.Unlock()
	}
	return out.list(), partial, nil
}

// listWriter writes a JSON list of entries as they come.
type listWriter struct {
	b bytes.Buffer
}

func (l *listWriter) add(entry []byte) {
	if l.b.Len() == 0 {
		l.b.WriteByte('[')
	} else {
		l.b.WriteByte(',')
	}
	l.b.Write(entry)
}

func (l *listWriter) list() []byte {
	if l.b.Len() == 0 {
		return []byte("[]")
	}
	l.b.WriteByte(']')
	return l.b.Bytes()
}

// mergedAPI answers an API request from every source: the catalogues
// merged, an entry's details from its source, anything else from the first
// source. partial says that a source failed.
func (c *Config) mergedAPI(ctx *gin.Context, params url.Values) (int, http.Header, []byte, bool, error) {
	action := params.Get("action")
	jsonHeader := http.Header{"Content-Type": {"application/json"}}
	for _, cat := range catalogues {
		switch action {
		case cat.categories:
			body, _, partial, err := c.mergedCategories(ctx, cat)
			return http.StatusOK, jsonHeader, body, partial, err
		case cat.list:
			body, partial, err := c.mergedList(ctx, cat, params)
			return http.StatusOK, jsonHeader, body, partial, err
		}
	}

	param, routed := routedActions[action]
	if !routed {
		status, header, body, err := c.providerGet(ctx, "player_api.php", params)
		return status, header, body, false, err
	}
	src, id := c.sourceOf(params.Get(param))
	asked := url.Values{}
	for k, v := range params {
		asked[k] = v
	}
	asked.Set(param, id)
	status, header, body, err := c.accountGet(ctx, src.account, "player_api.php", asked)
	if err != nil || src.index == 0 {
		return status, header, body, false, err
	}
	if u, ok := ctx.Get(userKey); ok {
		body = xtream.Sanitize(body, src.account, c.proxyAccount(u.(*proxyUser)))
	}
	if status == http.StatusOK {
		body = shownDetails(body, src.index)
	}
	return status, header, body, false, nil
}

// shownDetails gives the ids of an entry's details (a movie's stream, a
// series' episodes) as shown.
func shownDetails(body []byte, src int) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var answer map[string]any
	if dec.Decode(&answer) != nil {
		return body
	}
	shown := func(entry map[string]any, field string) {
		if v, ok := entry[field]; ok {
			entry[field] = xtream.SameType(v, shownID(src, xtream.Text(v)))
		}
	}
	if movie, ok := answer["movie_data"].(map[string]any); ok {
		shown(movie, "stream_id")
	}
	var seasons []any
	switch episodes := answer["episodes"].(type) {
	case map[string]any:
		for _, season := range episodes {
			seasons = append(seasons, season)
		}
	case []any:
		seasons = episodes
	}
	for _, season := range seasons {
		list, _ := season.([]any)
		for _, episode := range list {
			if e, ok := episode.(map[string]any); ok {
				shown(e, "id")
			}
		}
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(answer) != nil {
		return body
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// streamCandidates are where a stream asked under its shown id can be read:
// its source, then, for live television, the same channel elsewhere.
func (c *Config) streamCandidates(ctx *gin.Context, prefix, rest string, live bool) []candidate {
	dir, file := path.Split(rest)
	ext := path.Ext(file)
	shown := strings.TrimSuffix(file, ext)
	src, id := c.sourceOf(shown)
	candidates := []candidate{{src, src.account.StreamURL(prefix, dir+id+ext)}}
	if !live {
		return candidates
	}
	for _, other := range c.liveFallbacks(ctx, shown) {
		candidates = append(candidates, candidate{other.src, other.src.account.StreamURL(prefix, dir+other.id+ext)})
	}
	return candidates
}

// liveFallbacks returns the other sources of a live channel, reading the
// live lists again once they are older than the playlist cache.
func (c *Config) liveFallbacks(ctx *gin.Context, shown string) []sourceEntry {
	c.merge.mu.Lock()
	fresh := c.merge.fallbacks != nil && time.Since(c.merge.fallbacksAt) < time.Duration(c.M3UCacheExpiration)*time.Hour
	c.merge.mu.Unlock()
	if !fresh {
		if _, _, err := c.mergedList(ctx, catalogues[0], url.Values{}); err != nil {
			log.Printf("[iptv-proxy] live channels of the sources: %v", err)
		}
	}
	c.merge.mu.Lock()
	defer c.merge.mu.Unlock()
	return c.merge.fallbacks[shown]
}

// openFirst opens a stream at the first of its candidates that has a
// connection left and answers. A source at its limit is tried last.
func (c *Config) openFirst(candidates []candidate) opener {
	return func(ctx context.Context, header http.Header) (*http.Response, error) {
		order := make([]candidate, 0, len(candidates))
		var full []candidate
		for _, cand := range candidates {
			if c.sourceFull(ctx, cand.src) {
				full = append(full, cand)
			} else {
				order = append(order, cand)
			}
		}
		order = append(order, full...)

		for i, cand := range order {
			resp, err := c.upstreamDo(ctx, c.client, cand.url, header)
			last := i == len(order)-1
			if err == nil && resp.StatusCode < http.StatusBadRequest {
				return cand.src.hold(resp), nil
			}
			if last {
				return resp, err
			}
			why := failure(0, err, false)
			if err == nil {
				why = failure(resp.StatusCode, nil, false)
				_ = resp.Body.Close()
			}
			log.Printf("[iptv-proxy] %s failed (%s): trying %s", cand.src.name, why, order[i+1].src.name)
		}
		return nil, errors.New("no source for this stream")
	}
}

// sourceFull tells whether the proxy holds as many streams of a source as
// it allows.
func (c *Config) sourceFull(ctx context.Context, src *source) bool {
	src.mu.Lock()
	defer src.mu.Unlock()
	if !src.asked {
		src.asked = true
		src.limit = src.max
		if src.limit == 0 {
			src.limit = c.accountLimit(ctx, src.account)
		}
	}
	return src.limit > 0 && src.opened >= src.limit
}

// accountLimit asks the provider how many streams at once an account allows;
// 0 when it does not say.
func (c *Config) accountLimit(ctx context.Context, account xtream.Account) int {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	resp, err := c.upstreamDo(ctx, c.apiClient, account.APIURL("player_api.php", nil), http.Header{"User-Agent": {c.upstreamUserAgent("")}})
	if err != nil {
		return 0
	}
	defer resp.Body.Close() // nolint: errcheck
	var login struct {
		UserInfo struct {
			MaxConnections any `json:"max_connections"`
		} `json:"user_info"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxLoginBytes)).Decode(&login) != nil {
		return 0
	}
	n, _ := strconv.Atoi(xtream.Text(login.UserInfo.MaxConnections))
	return n
}

// hold counts a stream open at the source until its body is closed.
func (s *source) hold(resp *http.Response) *http.Response {
	s.mu.Lock()
	s.opened++
	s.mu.Unlock()
	resp.Body = &heldBody{ReadCloser: resp.Body, release: func() {
		s.mu.Lock()
		s.opened--
		s.mu.Unlock()
	}}
	return resp
}

type heldBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *heldBody) Close() error {
	b.once.Do(b.release)
	return b.ReadCloser.Close()
}

// sourceStream serves "<prefix><user>/<password>/<rest>" from the sources.
func (c *Config) sourceStream(ctx *gin.Context, prefix, rest string, live bool) {
	candidates := c.streamCandidates(ctx, prefix, rest, live)
	for i := range candidates {
		if ctx.Request.URL.RawQuery != "" {
			candidates[i].url += "?" + ctx.Request.URL.RawQuery
		}
	}
	open := c.openFirst(candidates)
	if live {
		c.streamLiveFrom(ctx, candidates[0].url, open)
		return
	}
	c.streamFrom(ctx, open)
}
