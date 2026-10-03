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
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// proxyUser is someone the proxy serves: the one of --user and --password, or
// each of the users of the configuration file.
type proxyUser struct {
	name, password config.CredentialString
	// max is how many streams the user may watch at once; 0 is no limit.
	max int
	// rules are the proxy's filters and the user's own.
	rules *filter.Rules
	slots slots
}

// reservedNames are the first elements of the proxy's own paths: a user
// named so would collide with them in stream addresses.
var reservedNames = map[string]bool{"hls": true, "logo": true, "live": true, "movie": true, "series": true, "timeshift": true, "play": true}

// setupUsers builds the users from the configuration.
func (c *Config) setupUsers() error {
	accounts := c.Users
	if len(accounts) == 0 {
		accounts = []config.User{{Name: c.User.String(), Password: c.Password.String(), MaxConnections: c.MaxConnections}}
	}

	c.usersByName = map[string]*proxyUser{}
	for _, account := range accounts {
		switch {
		case len(c.Users) > 0 && (account.Name == "" || account.Password == ""):
			return errors.New("every user needs a name and a password")
		case reservedNames[account.Name] || (c.endpointAntiColision != "" && account.Name == c.endpointAntiColision):
			return fmt.Errorf("user %q: the name is one of the proxy's own paths", account.Name)
		case c.usersByName[account.Name] != nil:
			return fmt.Errorf("user %q is defined twice", account.Name)
		}
		own, err := filter.New(account.Filter)
		if err != nil {
			return fmt.Errorf("user %q: %w", account.Name, err)
		}
		u := &proxyUser{
			name:     config.CredentialString(account.Name),
			password: config.CredentialString(account.Password),
			max:      account.MaxConnections,
			rules:    filter.Combine(c.rules, own),
		}
		c.users = append(c.users, u)
		c.usersByName[account.Name] = u
	}
	return nil
}

// userKey is where a request's user is kept in its context.
const userKey = "iptv-proxy user"

// userOf is the user a request was authenticated as.
func userOf(ctx *gin.Context) *proxyUser {
	return ctx.MustGet(userKey).(*proxyUser)
}

// as marks the requests of a route that names its user in its path.
func as(u *proxyUser) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Set(userKey, u) }
}

// authenticate checks the "username" and "password" of a request, given in
// its query or form, in any order.
func (c *Config) authenticate(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxFormBytes)
	if err := ctx.Request.ParseMultipartForm(maxFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		ctx.AbortWithError(http.StatusBadRequest, err) // nolint: errcheck
		return
	}

	username, password := ctx.Request.Form.Get("username"), ctx.Request.Form.Get("password")
	if username == "" || password == "" {
		ctx.AbortWithError(http.StatusBadRequest, errors.New("missing username or password")) // nolint: errcheck
		return
	}

	u := c.usersByName[username]
	expected := []byte{0} // compared all the same, so that time does not tell
	if u != nil {
		expected = []byte(u.password.String())
	}
	if subtle.ConstantTimeCompare([]byte(password), expected) != 1 || u == nil {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	ctx.Set(userKey, u)
}

// proxyAccount is the Xtream account a user sees: the proxy's address with
// the user's credentials.
func (c *Config) proxyAccount(u *proxyUser) xtream.Account {
	return xtream.Account{BaseURL: c.proxyBaseURL(), User: u.name.String(), Password: u.password.String()}
}

// slots are the streams a user is watching.
type slots struct {
	mu   sync.Mutex
	open []*slot // oldest first
}

type slot struct {
	ip     string
	stop   context.CancelFunc
	opened time.Time
}

// acquire takes a place for a new stream from ip. At the limit, the oldest
// stream of the same address gives its place (a player switching channels);
// a stream from another address is refused.
func (s *slots) acquire(limit int, ip string, stop context.CancelFunc) (*slot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit > 0 && len(s.open) >= limit {
		i := 0
		for i < len(s.open) && s.open[i].ip != ip {
			i++
		}
		if i == len(s.open) {
			return nil, false
		}
		s.open[i].stop()
		s.open = append(s.open[:i], s.open[i+1:]...)
	}
	sl := &slot{ip: ip, stop: stop, opened: time.Now()}
	s.open = append(s.open, sl)
	return sl, true
}

// release gives a place back. A place already taken over is not there any
// more.
func (s *slots) release(sl *slot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, o := range s.open {
		if o == sl {
			s.open = append(s.open[:i], s.open[i+1:]...)
			return
		}
	}
}

func (s *slots) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

// limit holds a stream request to its user's limit of streams at once.
func (c *Config) limit(ctx *gin.Context) {
	u := userOf(ctx)
	streamCtx, stop := context.WithCancel(ctx.Request.Context())
	defer stop()
	sl, ok := u.slots.acquire(u.max, ctx.ClientIP(), stop)
	if !ok {
		ctx.AbortWithError(http.StatusForbidden, fmt.Errorf("user %q: %d streams at once already", u.name, u.max)) // nolint: errcheck
		return
	}
	defer u.slots.release(sl)

	ctx.Request = ctx.Request.WithContext(streamCtx)
	ctx.Next()
}

// maskCredentials hides the users' names and passwords in a request
// address.
func (c *Config) maskCredentials(address string) string {
	path, query, hasQuery := strings.Cut(address, "?")

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		for _, u := range c.users {
			for _, secret := range []config.CredentialString{u.name, u.password} {
				if secret != "" && (segment == secret.String() || segment == secret.PathEscape()) {
					segments[i] = "***"
				}
			}
		}
	}
	path = strings.Join(segments, "/")
	if !hasQuery {
		return path
	}

	params := strings.Split(query, "&")
	for i, param := range params {
		if name, _, hasValue := strings.Cut(param, "="); hasValue && (name == "username" || name == "password") {
			params[i] = name + "=***"
		}
	}
	return path + "?" + strings.Join(params, "&")
}
