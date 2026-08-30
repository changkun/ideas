// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"strings"

	"latere.ai/x/pkg/jwtauth"
)

// latereVerifier accepts the RS256 access tokens that auth.latere.ai issues to
// the blog compose box through browser PKCE.
//
// Signature, issuer and expiry come from the JWKS document. Posting rights do
// not: any latere account can mint a token for any client, so a valid
// signature only proves *who* is calling. The allowlist decides whether that
// principal may write to the blog.
type latereVerifier struct {
	validator *jwtauth.Validator
	allowed   map[string]bool // lowercased email or principal id (sub)
	log       *log.Logger
}

// newLatereVerifier builds a verifier from the environment. It returns nil
// when AUTH_ALLOWED_PRINCIPALS is unset: with no allowlist there is no safe
// answer, so latere tokens are refused outright rather than trusted wholesale.
func newLatereVerifier(l *log.Logger) *latereVerifier {
	allowed := principalSet(os.Getenv("AUTH_ALLOWED_PRINCIPALS"))
	if len(allowed) == 0 {
		l.Println("AUTH_ALLOWED_PRINCIPALS is unset, latere tokens will be rejected")
		return nil
	}

	issuer := strings.TrimRight(cmp.Or(os.Getenv("AUTH_URL"), "https://auth.latere.ai"), "/")
	jwks := cmp.Or(os.Getenv("AUTH_JWKS_URL"), issuer+"/.well-known/jwks.json")
	l.Printf("latere auth enabled: issuer=%s principals=%d", issuer, len(allowed))

	return &latereVerifier{
		validator: jwtauth.New(jwtauth.Config{JWKSURL: jwks, Issuer: issuer}),
		allowed:   allowed,
		log:       l,
	}
}

// principalSet parses a comma-separated list of emails and principal ids.
func principalSet(s string) map[string]bool {
	set := map[string]bool{}
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			set[p] = true
		}
	}
	return set
}

// allow reports whether token is a latere token belonging to an allowlisted
// principal. A false result covers both "not a latere token" and "not
// permitted", so callers may fall through to another scheme; the two are
// distinguished in the log.
func (v *latereVerifier) allow(token string) bool {
	if v == nil {
		return false
	}
	claims, err := v.validator.Validate(token)
	if err != nil {
		return false
	}
	if v.allowed[strings.ToLower(claims.Email)] || v.allowed[strings.ToLower(claims.Sub)] {
		return true
	}
	v.log.Printf("latere principal not allowed: sub=%s email=%s client=%s",
		claims.Sub, claims.Email, claims.ClientID)
	return false
}

// auth admits one credential: a latere access token carried as a Bearer.
// Everything else is rejected.
func auth(latere *latereVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ideas/ping" {
			next.ServeHTTP(w, r)
			return
		}

		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			if latere.allow(strings.TrimPrefix(h, "Bearer ")) {
				next.ServeHTTP(w, r)
				return
			}
		}

		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
