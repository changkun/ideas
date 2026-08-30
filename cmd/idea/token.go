// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"latere.ai/x/pkg/oidc"
)

// latereClientID is the public OIDC client whose refresh tokens sit in
// auth-token.json. The refresh grant is bound to the client that issued the
// token, so this must stay in step with latere-cli.
const latereClientID = "latere-cli"

// refreshSkew renews a token slightly before its recorded expiry, so a token
// that dies mid-flight does not turn into a 401 from the ideas API.
const refreshSkew = 60 * time.Second

// errNoToken means no credential is on disk at all.
var errNoToken = errors.New("not signed in; run `latere login`")

// latereToken is the on-disk shape latere-cli writes. It must stay
// field-for-field with latere-cli/internal/api.Token: this program writes the
// file back after a refresh, so a field missing here is a field latere-cli
// loses.
//
// It is deliberately not an oauth2.Token. latere records the expiry as
// "expires_at", and decoding that file into an oauth2.Token would leave Expiry
// zero, making a long-dead token look valid forever.
type latereToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	IssuedAt     time.Time `json:"issued_at"`
}

// fresh reports whether the access token can be used without renewal. A zero
// ExpiresAt is not fresh: an unrecorded lifetime is unknown, not unlimited.
func (t latereToken) fresh(now time.Time) bool {
	return t.AccessToken != "" && !t.ExpiresAt.IsZero() &&
		now.Before(t.ExpiresAt.Add(-refreshSkew))
}

// authTokenPath resolves latere's auth-token.json.
//
// latere keeps two token files side by side and only this one fits here.
// token.json holds a bearer minted by cella.latere.ai for its own API;
// auth-token.json holds the auth.latere.ai token pair, and the ideas API
// verifies its bearer against that issuer's JWKS. Reaching for the shorter
// name yields a token with the wrong "iss" and an unexplained 401.
//
// LATERE_AUTH_TOKEN_FILE overrides, matching latere-cli.
func authTokenPath() string {
	if v := os.Getenv("LATERE_AUTH_TOKEN_FILE"); v != "" {
		return v
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "latere", "auth-token.json")
}

// loadLatereToken reads path. A missing file reports errNoToken, since that
// is the ordinary "never logged in" case rather than a fault.
func loadLatereToken(path string) (latereToken, error) {
	if path == "" {
		return latereToken{}, errNoToken
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return latereToken{}, errNoToken
		}
		return latereToken{}, fmt.Errorf("read %s: %w", path, err)
	}
	var t latereToken
	if err := json.Unmarshal(b, &t); err != nil {
		return latereToken{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if t.AccessToken == "" {
		return latereToken{}, errNoToken
	}
	return t, nil
}

// saveLatereToken writes path atomically with 0600, creating the directory
// with 0700 if missing.
func saveLatereToken(path string, t latereToken) error {
	if path == "" {
		return errors.New("cannot determine the latere token path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp token file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp token file: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// bearerToken returns an access token for the ideas API, renewing it first
// when the cached one is spent. Access tokens live 15 minutes, so a cached
// credential is stale far more often than not and refresh is the normal path.
func bearerToken(ctx context.Context, authURL, path string) (string, error) {
	tok, err := loadLatereToken(path)
	if err != nil {
		return "", err
	}
	if tok.fresh(time.Now()) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		if tok.ExpiresAt.IsZero() {
			// No expiry was recorded and there is nothing to renew with.
			// Send what we have and let the server judge it.
			return tok.AccessToken, nil
		}
		return "", fmt.Errorf("latere token expired at %s and carries no refresh token; run `latere login`",
			tok.ExpiresAt.Format(time.RFC3339))
	}

	refreshed, err := refreshLatereToken(ctx, authURL, tok.RefreshToken)
	if err != nil {
		return "", err
	}
	// The auth service deactivates the old refresh token on use, so losing
	// the replacement would strand every latere command, not just this one.
	// Persisting is part of the refresh, not a best-effort afterthought.
	if err := saveLatereToken(path, refreshed); err != nil {
		return "", fmt.Errorf("latere token was refreshed but could not be saved, "+
			"the previous one is now void: %w", err)
	}
	return refreshed.AccessToken, nil
}

// refreshLatereToken exchanges a refresh token for a new pair. The refresh
// grant carries no scope parameter, so the token keeps the scopes it was
// issued with and none need to be named here.
func refreshLatereToken(ctx context.Context, authURL, refreshToken string) (latereToken, error) {
	c := oidc.New(oidc.Config{
		AuthURL:  strings.TrimRight(authURL, "/"),
		ClientID: latereClientID,
	})
	if c == nil {
		return latereToken{}, errors.New("cannot build the latere OIDC client")
	}
	tok, err := c.RefreshTokenContext(ctx, refreshToken)
	if err != nil {
		return latereToken{}, fmt.Errorf("refresh latere token: %w, run `latere login`", err)
	}
	return latereToken{
		AccessToken: tok.AccessToken,
		// An auth server that does not rotate returns no replacement; keep
		// the one that still works rather than writing an empty field.
		RefreshToken: cmp.Or(tok.RefreshToken, refreshToken),
		TokenType:    "Bearer",
		ExpiresAt:    tok.Expiry,
		IssuedAt:     time.Now().UTC(),
	}, nil
}
