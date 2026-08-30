// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeToken puts tok in a temp auth-token.json and returns its path.
func writeToken(t *testing.T, tok latereToken) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "auth-token.json")
	b, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("cannot encode token: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("cannot write token: %v", err)
	}
	return path
}

// authStub is an auth.latere.ai stand-in serving only the token endpoint the
// refresh grant needs.
type authStub struct {
	url  string
	form url.Values // the last refresh request, for assertions
}

func newAuthStub(t *testing.T, reply map[string]any, status int) *authStub {
	t.Helper()

	s := &authStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("cannot parse form: %v", err)
		}
		s.form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// TestAuthTokenPath pins the file the CLI reads. Picking token.json instead
// would yield a cella.latere.ai bearer, which the ideas API rejects for a
// wrong "iss" without saying why.
func TestAuthTokenPath(t *testing.T) {
	t.Setenv("LATERE_AUTH_TOKEN_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := authTokenPath(), "/xdg/latere/auth-token.json"; got != want {
		t.Fatalf("authTokenPath() = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if got, want := authTokenPath(), filepath.Join(home, ".config", "latere", "auth-token.json"); got != want {
		t.Fatalf("authTokenPath() = %q, want %q", got, want)
	}

	t.Setenv("LATERE_AUTH_TOKEN_FILE", "/custom/tok.json")
	if got, want := authTokenPath(), "/custom/tok.json"; got != want {
		t.Fatalf("authTokenPath() = %q, want %q", got, want)
	}
}

// TestLoadLatereToken separates "never logged in" from a real read fault, so
// the CLI can tell the user which one happened.
func TestLoadLatereToken(t *testing.T) {
	if _, err := loadLatereToken(filepath.Join(t.TempDir(), "absent.json")); !errors.Is(err, errNoToken) {
		t.Fatalf("missing file err = %v, want errNoToken", err)
	}
	if _, err := loadLatereToken(""); !errors.Is(err, errNoToken) {
		t.Fatalf("empty path err = %v, want errNoToken", err)
	}
	if _, err := loadLatereToken(writeToken(t, latereToken{})); !errors.Is(err, errNoToken) {
		t.Fatalf("empty token err = %v, want errNoToken", err)
	}

	corrupt := filepath.Join(t.TempDir(), "auth-token.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := loadErr(t, corrupt)
	if errors.Is(err, errNoToken) || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("corrupt file err = %v, want a parse error", err)
	}
}

func loadErr(t *testing.T, path string) error {
	t.Helper()
	_, err := loadLatereToken(path)
	if err == nil {
		t.Fatal("loadLatereToken() = nil error, want one")
	}
	return err
}

// TestLatereTokenFresh covers the expiry field that makes this type necessary.
// latere records the expiry as "expires_at"; decoding the same file into an
// oauth2.Token leaves Expiry zero, so a token dead for weeks reads as valid.
// Anything without a live "expires_at" must go through refresh instead.
func TestLatereTokenFresh(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		tok  latereToken
		want bool
	}{
		{
			name: "live expiry",
			tok:  latereToken{AccessToken: "a", ExpiresAt: now.Add(10 * time.Minute)},
			want: true,
		},
		{
			name: "expired long ago",
			tok:  latereToken{AccessToken: "a", ExpiresAt: now.Add(-30 * 24 * time.Hour)},
			want: false,
		},
		{
			name: "inside the refresh skew",
			tok:  latereToken{AccessToken: "a", ExpiresAt: now.Add(refreshSkew / 2)},
			want: false,
		},
		{
			name: "no recorded expiry is unknown, not unlimited",
			tok:  latereToken{AccessToken: "a"},
			want: false,
		},
		{
			name: "no access token",
			tok:  latereToken{ExpiresAt: now.Add(time.Hour)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.fresh(now); got != tt.want {
				t.Fatalf("fresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLatereTokenDecodesExpiresAt is the counterpart on the wire format: the
// field is "expires_at", not oauth2's "expiry".
func TestLatereTokenDecodesExpiresAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-token.json")
	raw := `{"access_token":"a","token_type":"Bearer",` +
		`"expires_at":"2026-07-26T18:44:00Z","issued_at":"2026-07-26T18:29:00Z"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	tok, err := loadLatereToken(path)
	if err != nil {
		t.Fatalf("loadLatereToken() error: %v", err)
	}
	want := time.Date(2026, 7, 26, 18, 44, 0, 0, time.UTC)
	if !tok.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", tok.ExpiresAt, want)
	}
	if tok.fresh(time.Now()) {
		t.Fatal("fresh() = true for a token expired in the past")
	}
}

// TestBearerTokenFresh keeps a live token off the network.
func TestBearerTokenFresh(t *testing.T) {
	stub := newAuthStub(t, map[string]any{}, http.StatusInternalServerError)
	path := writeToken(t, latereToken{
		AccessToken:  "live-token",
		RefreshToken: "r1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	got, err := bearerToken(context.Background(), stub.url, path)
	if err != nil {
		t.Fatalf("bearerToken() error: %v", err)
	}
	if got != "live-token" {
		t.Fatalf("bearerToken() = %q, want live-token", got)
	}
	if stub.form != nil {
		t.Fatal("bearerToken() called the auth service for a live token")
	}
}

// TestBearerTokenRefreshes is the ordinary path: access tokens live 15
// minutes, so the cached one is usually spent. The rotated pair must reach
// disk, because the auth service deactivates the refresh token it replaces.
func TestBearerTokenRefreshes(t *testing.T) {
	stub := newAuthStub(t, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "r2",
		"token_type":    "Bearer",
		"expires_in":    900,
	}, http.StatusOK)
	path := writeToken(t, latereToken{
		AccessToken:  "stale",
		RefreshToken: "r1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})

	got, err := bearerToken(context.Background(), stub.url, path)
	if err != nil {
		t.Fatalf("bearerToken() error: %v", err)
	}
	if got != "new-access" {
		t.Fatalf("bearerToken() = %q, want new-access", got)
	}
	if v := stub.form.Get("grant_type"); v != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", v)
	}
	if v := stub.form.Get("refresh_token"); v != "r1" {
		t.Fatalf("refresh_token = %q, want r1", v)
	}
	if v := stub.form.Get("client_id"); v != latereClientID {
		t.Fatalf("client_id = %q, want %q", v, latereClientID)
	}

	saved, err := loadLatereToken(path)
	if err != nil {
		t.Fatalf("loadLatereToken() error: %v", err)
	}
	if saved.AccessToken != "new-access" || saved.RefreshToken != "r2" {
		t.Fatalf("saved = %+v, want the rotated pair", saved)
	}
	if !saved.fresh(time.Now()) {
		t.Fatalf("saved token is not fresh, ExpiresAt = %v", saved.ExpiresAt)
	}
}

// TestBearerTokenKeepsRefreshToken covers an auth service that does not
// rotate: writing back an empty field would discard the only way to renew.
func TestBearerTokenKeepsRefreshToken(t *testing.T) {
	stub := newAuthStub(t, map[string]any{
		"access_token": "new-access",
		"token_type":   "Bearer",
		"expires_in":   900,
	}, http.StatusOK)
	path := writeToken(t, latereToken{
		AccessToken:  "stale",
		RefreshToken: "r1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})

	if _, err := bearerToken(context.Background(), stub.url, path); err != nil {
		t.Fatalf("bearerToken() error: %v", err)
	}
	saved, err := loadLatereToken(path)
	if err != nil {
		t.Fatalf("loadLatereToken() error: %v", err)
	}
	if saved.RefreshToken != "r1" {
		t.Fatalf("RefreshToken = %q, want the original r1", saved.RefreshToken)
	}
}

// TestBearerTokenRefreshRejected points the user at `latere login` when the
// refresh token is spent too.
func TestBearerTokenRefreshRejected(t *testing.T) {
	stub := newAuthStub(t, map[string]any{"error": "invalid_grant"}, http.StatusBadRequest)
	path := writeToken(t, latereToken{
		AccessToken:  "stale",
		RefreshToken: "r1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})

	_, err := bearerToken(context.Background(), stub.url, path)
	if err == nil {
		t.Fatal("bearerToken() = nil error, want one")
	}
	if !strings.Contains(err.Error(), "latere login") {
		t.Fatalf("error = %v, want it to name `latere login`", err)
	}
}

// TestBearerTokenExpiredWithoutRefresh reports the dead credential rather
// than sending it and letting the API answer with a bare 401.
func TestBearerTokenExpiredWithoutRefresh(t *testing.T) {
	path := writeToken(t, latereToken{
		AccessToken: "stale",
		ExpiresAt:   time.Now().Add(-time.Hour),
	})

	_, err := bearerToken(context.Background(), "https://auth.invalid", path)
	if err == nil {
		t.Fatal("bearerToken() = nil error, want one")
	}
	if !strings.Contains(err.Error(), "latere login") {
		t.Fatalf("error = %v, want it to name `latere login`", err)
	}
}

// TestBearerTokenUnknownExpiryWithoutRefresh sends what is on disk: nothing
// can renew it and nothing says it is dead, so the server decides.
func TestBearerTokenUnknownExpiryWithoutRefresh(t *testing.T) {
	path := writeToken(t, latereToken{AccessToken: "unknown-expiry"})

	got, err := bearerToken(context.Background(), "https://auth.invalid", path)
	if err != nil {
		t.Fatalf("bearerToken() error: %v", err)
	}
	if got != "unknown-expiry" {
		t.Fatalf("bearerToken() = %q, want unknown-expiry", got)
	}
}

// TestBearerTokenNoFile is the never-logged-in case.
func TestBearerTokenNoFile(t *testing.T) {
	_, err := bearerToken(context.Background(), "https://auth.invalid",
		filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, errNoToken) {
		t.Fatalf("bearerToken() err = %v, want errNoToken", err)
	}
}

// TestSaveLatereTokenPermissions keeps the credential unreadable by other
// users and creates the directory when latere has never run.
func TestSaveLatereTokenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latere", "auth-token.json")
	if err := saveLatereToken(path, latereToken{AccessToken: "a", ExpiresAt: time.Now()}); err != nil {
		t.Fatalf("saveLatereToken() error: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %v, want 0600", got)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", got)
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	} else if len(entries) != 1 {
		t.Fatalf("dir holds %d entries, want only the token file", len(entries))
	}
}

// TestBearerTokenPersistFailureIsFatal is the reason saving is not
// best-effort. The auth service deactivates the refresh token it replaces, so
// a refresh that never reaches disk strands every latere command, not just
// this one. Returning the token anyway would hide that.
func TestBearerTokenPersistFailureIsFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	stub := newAuthStub(t, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "r2",
		"token_type":    "Bearer",
		"expires_in":    900,
	}, http.StatusOK)

	dir := t.TempDir()
	path := writeToken(t, latereToken{
		AccessToken:  "stale",
		RefreshToken: "r1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})
	path = filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(path, []byte(`{"access_token":"stale","refresh_token":"r1",`+
		`"expires_at":"2020-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := bearerToken(context.Background(), stub.url, path)
	if err == nil {
		t.Fatal("bearerToken() = nil error, want the persist failure surfaced")
	}
	if !strings.Contains(err.Error(), "void") {
		t.Fatalf("error = %v, want it to say the previous token is void", err)
	}
}

// TestSaveLatereTokenNoPath guards the degrade path: no config dir resolves
// to an empty path, which must fail rather than write somewhere arbitrary.
func TestSaveLatereTokenNoPath(t *testing.T) {
	if err := saveLatereToken("", latereToken{AccessToken: "a"}); err == nil {
		t.Fatal("saveLatereToken(\"\") = nil error, want one")
	}
}
