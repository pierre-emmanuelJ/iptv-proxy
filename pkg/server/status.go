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
	"crypto/subtle"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

// The status page (--status-password) tells what the proxy is doing: the
// streams being watched, the sources and their connections, the tuner. It
// changes nothing, and names no credentials: stream addresses are masked,
// sources are named by their host.

// started is when the proxy started.
var started = time.Now()

type status struct {
	Version     string         `json:"version"`
	Started     time.Time      `json:"started"`
	Uptime      string         `json:"uptime"`
	Users       []statusUser   `json:"users"`
	Watched     int            `json:"streams_watched"`
	Passthrough int            `json:"passthrough_accounts,omitempty"`
	Shared      int            `json:"provider_streams_shared"`
	Sources     []statusSource `json:"sources"`
	Tuner       *statusTuner   `json:"tuner,omitempty"`
	Kept        int            `json:"kept_answers"`
	KeptBytes   int            `json:"kept_answers_bytes"`
}

type statusUser struct {
	Name    string         `json:"name"`
	Limit   int            `json:"max_connections"`
	Streams []statusStream `json:"streams"`
}

type statusStream struct {
	Address string    `json:"address"`
	From    string    `json:"from"`
	Since   time.Time `json:"since"`
	For     string    `json:"for"`
}

type statusSource struct {
	Name  string `json:"name"`
	Host  string `json:"host"`
	Open  int    `json:"streams_open"`
	Limit int    `json:"max_connections"`
}

type statusTuner struct {
	Port     int `json:"port"`
	Channels int `json:"channels"`
}

// statusAuth lets in a request with the status password, whatever its user
// name (HTTP basic authentication).
func (c *Config) statusAuth(ctx *gin.Context) {
	_, password, ok := ctx.Request.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(password), []byte(c.StatusPassword.String())) != 1 {
		ctx.Header("WWW-Authenticate", `Basic realm="iptv-proxy status"`)
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

// currentStatus gathers the status.
func (c *Config) currentStatus() status {
	now := time.Now()
	s := status{Version: c.Version, Started: started, Uptime: now.Sub(started).Round(time.Second).String(), Shared: c.hub.Streams()}

	users := c.users
	c.passthrough.mu.Lock()
	for _, a := range c.passthrough.accounts {
		a.mu.Lock()
		if a.user != nil {
			users = append(users, a.user)
			s.Passthrough++
		}
		a.mu.Unlock()
	}
	c.passthrough.mu.Unlock()
	for i, u := range users {
		su := statusUser{Name: u.name.String(), Limit: u.max, Streams: []statusStream{}}
		if i >= len(c.users) {
			su.Name = "account " + u.name.String()
		}
		u.slots.mu.Lock()
		for _, sl := range u.slots.open {
			su.Streams = append(su.Streams, statusStream{Address: sl.what, From: sl.ip, Since: sl.opened, For: now.Sub(sl.opened).Round(time.Second).String()})
		}
		s.Watched += len(su.Streams)
		u.slots.mu.Unlock()
		s.Users = append(s.Users, su)
	}
	sort.SliceStable(s.Users[len(c.users):], func(i, j int) bool {
		return s.Users[len(c.users)+i].Name < s.Users[len(c.users)+j].Name
	})

	// Connections are counted by source when there are several.
	for _, src := range c.sources {
		host := ""
		if address, err := url.Parse(src.account.BaseURL); err == nil {
			host = address.Host
		}
		src.mu.Lock()
		s.Sources = append(s.Sources, statusSource{Name: src.name, Host: host, Open: src.opened, Limit: src.limit})
		src.mu.Unlock()
	}

	if c.HDHomeRunPort != 0 {
		c.tuner.mu.Lock()
		s.Tuner = &statusTuner{Port: c.HDHomeRunPort, Channels: len(c.tuner.channels)}
		c.tuner.mu.Unlock()
	}

	c.lastGood.mu.Lock()
	s.Kept, s.KeptBytes = len(c.lastGood.answers), c.lastGood.size
	c.lastGood.mu.Unlock()
	return s
}

func (c *Config) statusJSON(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, c.currentStatus())
}

func (c *Config) statusHTML(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Type", "text/html; charset=utf-8")
	ctx.Status(http.StatusOK)
	_ = statusTemplate.Execute(ctx.Writer, c.currentStatus())
}

var statusTemplate = template.Must(template.New("status").Funcs(template.FuncMap{"size": formatBytes}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="10">
<title>iptv-proxy status</title>
<style>
:root { color-scheme: light dark; --fg: #1d1d1f; --bg: #fafafa; --muted: #6e6e73; --line: #d2d2d7; }
@media (prefers-color-scheme: dark) { :root { --fg: #f5f5f7; --bg: #161617; --muted: #a1a1a6; --line: #3a3a3c; } }
body { margin: 0 auto; max-width: 960px; padding: 16px; font: 15px/1.45 system-ui, sans-serif; color: var(--fg); background: var(--bg); }
h1 { font-size: 1.3em; margin: 0 0 4px; } h2 { font-size: 1.05em; margin: 24px 0 8px; }
.muted { color: var(--muted); } table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid var(--line); vertical-align: top; overflow-wrap: anywhere; }
th { font-weight: 600; color: var(--muted); font-size: .9em; }
</style></head><body>
<h1>iptv-proxy {{.Version}}</h1>
<p class="muted">Up for {{.Uptime}} · {{.Shared}} live stream(s) open at the provider, shared between their viewers · refreshed every 10 s</p>

<h2>Streams being watched</h2>
<table><tr><th>User</th><th>Stream</th><th>From</th><th>For</th></tr>
{{range .Users}}{{$u := .}}{{range .Streams}}<tr><td>{{$u.Name}}</td><td>{{.Address}}</td><td>{{.From}}</td><td>{{.For}}</td></tr>
{{end}}{{end}}{{if not .Watched}}<tr><td colspan="4" class="muted">Nobody is watching.</td></tr>{{end}}</table>

<h2>Users</h2>
<table><tr><th>User</th><th>Streams</th><th>Limit</th></tr>
{{range .Users}}<tr><td>{{.Name}}</td><td>{{len .Streams}}</td><td>{{if .Limit}}{{.Limit}}{{else}}none{{end}}</td></tr>
{{end}}</table>

{{if .Sources}}<h2>Sources</h2>
<table><tr><th>Source</th><th>Host</th><th>Streams open</th><th>Limit</th></tr>
{{range .Sources}}<tr><td>{{.Name}}</td><td>{{.Host}}</td><td>{{.Open}}</td><td>{{if .Limit}}{{.Limit}}{{else}}-{{end}}</td></tr>
{{end}}</table>{{end}}

{{with .Tuner}}<h2>HDHomeRun tuner</h2>
<p>Port {{.Port}} · {{.Channels}} channel(s) in its last lineup</p>{{end}}

<h2>Kept answers</h2>
<p>{{.Kept}} answer(s), {{size .KeptBytes}} compressed, served when the provider fails</p>
</body></html>
`))

// formatBytes writes a size for people.
func formatBytes(n int) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}
