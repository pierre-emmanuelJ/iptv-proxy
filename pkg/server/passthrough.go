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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// In passthrough mode (--xtream-passthrough) the proxy has no users of its
// own: a client logs in with its account of the Xtream provider, which the
// proxy checks with the provider, then uses for that client's requests.

// accountCheck is how long an account the provider accepted is trusted
// before asking the provider again.
var accountCheck = 10 * time.Minute

// maxLoginBytes bounds the provider's login answer.
const maxLoginBytes = 1 << 20

// errRefused is the provider refusing an account.
var errRefused = errors.New("the provider refused this account")

type passthrough struct {
	mu sync.Mutex
	// accounts are the accounts the provider accepted, by a hash of their
	// credentials, and the ones being checked.
	accounts map[[sha256.Size]byte]*passthroughAccount
}

type passthroughAccount struct {
	mu      sync.Mutex // one check at a time
	user    *proxyUser // nil until the provider accepts the account
	checked time.Time
}

// setupPassthrough checks the settings of passthrough mode.
func (c *Config) setupPassthrough() error {
	switch {
	case c.XtreamBaseURL == "":
		return errors.New("--xtream-passthrough needs the provider's address (--xtream-base-url)")
	case c.XtreamUser != "" || c.XtreamPassword != "":
		return errors.New("--xtream-passthrough takes the provider account from each client: leave out --xtream-user and --xtream-password")
	case c.RemoteURL != nil && c.RemoteURL.String() != "":
		return errors.New("--xtream-passthrough works with an Xtream provider, not with --m3u-url")
	case len(c.Users) > 0:
		return errors.New("--xtream-passthrough has no users of its own: leave out the users of the configuration file")
	case c.HDHomeRunPort != 0:
		return errors.New("the HDHomeRun tuner needs an account of the provider: it does not work with --xtream-passthrough")
	}
	c.passthrough.accounts = map[[sha256.Size]byte]*passthroughAccount{}
	return nil
}

// passthroughUser returns the user of an account of the provider. The
// provider is asked when the account is new to the proxy or was checked a
// while ago; an account it accepted before stays accepted while it fails.
func (c *Config) passthroughUser(ctx *gin.Context, name, password string) (*proxyUser, error) {
	key := sha256.Sum256([]byte(name + "\x00" + password))
	c.passthrough.mu.Lock()
	a := c.passthrough.accounts[key]
	if a == nil {
		a = &passthroughAccount{}
		c.passthrough.accounts[key] = a
	}
	c.passthrough.mu.Unlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.user != nil && time.Since(a.checked) < accountCheck {
		return a.user, nil
	}

	account := xtream.Account{BaseURL: c.XtreamBaseURL, User: name, Password: password}
	allowed, err := c.checkAccount(ctx, account)
	if err != nil && a.user != nil && !errors.Is(err, errRefused) {
		log.Printf("[iptv-proxy] the provider could not check an account (%v): it stays accepted", err)
		return a.user, nil
	}
	if err != nil {
		// Only the accounts the provider accepts are remembered.
		c.passthrough.mu.Lock()
		if c.passthrough.accounts[key] == a {
			delete(c.passthrough.accounts, key)
		}
		c.passthrough.mu.Unlock()
		a.user = nil
		return nil, err
	}

	if a.user == nil {
		if c.MaxConnections > 0 {
			allowed = c.MaxConnections
		}
		a.user = &proxyUser{
			name:     config.CredentialString(name),
			password: config.CredentialString(password),
			max:      allowed,
			rules:    c.rules,
			provider: account,
		}
	}
	a.checked = time.Now()
	return a.user, nil
}

// checkAccount asks the provider whether it accepts an account, and returns
// how many streams at once the account allows (0 when it does not say).
func (c *Config) checkAccount(ctx *gin.Context, account xtream.Account) (int, error) {
	resp, err := c.upstream(ctx, c.apiClient, account.APIURL("player_api.php", nil))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() // nolint: errcheck

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return 0, errRefused
	case resp.StatusCode != http.StatusOK:
		return 0, fmt.Errorf("login: the provider answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginBytes))
	if err != nil {
		return 0, err
	}
	if !json.Valid(body) {
		return 0, errors.New("login: the provider did not answer with JSON")
	}
	var login struct {
		UserInfo struct {
			Auth           any `json:"auth"`
			MaxConnections any `json:"max_connections"`
		} `json:"user_info"`
	}
	// An answer of another shape (an empty list, some providers) is a
	// refusal too.
	if json.Unmarshal(body, &login) != nil {
		return 0, errRefused
	}
	if auth := xtream.Text(login.UserInfo.Auth); auth != "1" && auth != "true" {
		return 0, errRefused
	}
	allowed, _ := strconv.Atoi(xtream.Text(login.UserInfo.MaxConnections))
	return allowed, nil
}

// passthroughLogin authenticates a request as the account it names, and
// answers it when it cannot.
func (c *Config) passthroughLogin(ctx *gin.Context, name, password string) {
	u, err := c.passthroughUser(ctx, name, password)
	switch {
	case errors.Is(err, errRefused):
		ctx.AbortWithStatus(http.StatusUnauthorized)
	case err != nil:
		c.upstreamError(ctx, err)
	default:
		ctx.Set(userKey, u)
	}
}

// passthroughStream authenticates a stream request as the account in its
// path.
func (c *Config) passthroughStream(ctx *gin.Context) {
	c.passthroughLogin(ctx, ctx.Param("username"), ctx.Param("password"))
}
