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
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// addressTokens turns a provider address into a token a client can hand
// back, and a token into its address.
//
// An HLS playlist names what the player must fetch next (variants, segments,
// keys), on any host and often with the provider's credentials or a session
// token in the address. The proxy serves each of them under a token instead:
// the address is encrypted, so a client neither learns it nor can make the
// proxy fetch an address of its own choosing. Nothing is stored: a token is
// all the proxy needs, even after a restart.
type addressTokens struct {
	aead   cipher.AEAD
	macKey []byte
}

var errBadToken = errors.New("invalid stream token")

// newAddressTokens derives the keys from what only this proxy's owner knows
// (its credentials and its provider's): tokens survive a restart, and are
// worthless to a proxy configured differently.
func newAddressTokens(secrets ...string) (*addressTokens, error) {
	derive := func(purpose string) []byte {
		h := sha256.New()
		h.Write([]byte("iptv-proxy address token v1\x00" + purpose))
		for _, s := range secrets {
			h.Write([]byte{0})
			h.Write([]byte(s))
		}
		return h.Sum(nil)
	}

	block, err := aes.NewCipher(derive("encryption"))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &addressTokens{aead: aead, macKey: derive("nonce")}, nil
}

// seal returns the token of an address. The same address always gives the
// same token, so that a player recognizes a segment it already has from one
// playlist refresh to the next: the nonce is derived from the address, which
// is safe as two different addresses never share one.
func (t *addressTokens) seal(address string) string {
	mac := hmac.New(sha256.New, t.macKey)
	mac.Write([]byte(address))
	nonce := mac.Sum(nil)[:t.aead.NonceSize()]

	return base64.RawURLEncoding.EncodeToString(t.aead.Seal(nonce, nonce, []byte(address), nil))
}

// open returns the address of a token this proxy issued.
func (t *addressTokens) open(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < t.aead.NonceSize() {
		return "", errBadToken
	}
	nonce, sealed := raw[:t.aead.NonceSize()], raw[t.aead.NonceSize():]

	address, err := t.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", errBadToken
	}
	return string(address), nil
}
