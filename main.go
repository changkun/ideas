// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"changkun.de/x/login"
)

func main() {
	l := log.New(os.Stdout, "ideas: ", log.LstdFlags|log.Lshortfile|log.Lmsgprefix)

	if v := os.Getenv("LOGIN_VERIFY_URL"); v != "" {
		login.VerifyEndpoint = v
	}

	llmBaseURL := os.Getenv("LLM_BASE_URL")
	if llmBaseURL == "" {
		l.Fatal("LLM_BASE_URL is required")
	}
	llmAPIKey := os.Getenv("LLM_API_KEY")
	if llmAPIKey == "" {
		l.Fatal("LLM_API_KEY is required")
	}
	gitToken := os.Getenv("GIT_TOKEN")
	if gitToken == "" {
		l.Fatal("GIT_TOKEN is required")
	}

	gitRepo := cmp.Or(os.Getenv("GIT_REPO"), "changkun/blog")
	parts := strings.SplitN(gitRepo, "/", 2)
	if len(parts) != 2 {
		l.Fatalf("GIT_REPO must be in owner/repo format, got: %s", gitRepo)
	}

	svc := &service{
		log: l,
		llm: &llmClient{
			baseURL:    llmBaseURL,
			apiKey:     llmAPIKey,
			model:      cmp.Or(os.Getenv("LLM_MODEL"), "anthropic/claude-sonnet-4-5-20250929"),
			titleModel: cmp.Or(os.Getenv("LLM_TITLE_MODEL"), "anthropic/claude-haiku-4-5-20251001"),
			log:        l,
		},
		github: &githubClient{
			token: gitToken,
			owner: parts[0],
			repo:  parts[1],
			name:  cmp.Or(os.Getenv("GIT_COMMITTER_NAME"), "Changkun Ideas API Server"),
			email: cmp.Or(os.Getenv("GIT_COMMITTER_EMAIL"), "hi+ideas@changkun.de"),
		},
	}

	r := http.NewServeMux()
	r.HandleFunc("GET /ideas/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "pong")
	})
	r.HandleFunc("POST /ideas/post", svc.handlePost)
	r.HandleFunc("POST /ideas/improve", svc.handleImprove)

	addr := cmp.Or(os.Getenv("IDEAS_ADDR"), "0.0.0.0:80")
	s := &http.Server{
		Addr:         addr,
		Handler:      logging(l)(cors(auth(r))),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 2 * time.Minute,
		IdleTimeout:  time.Minute,
	}

	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	go func() {
		<-quit
		l.Println("ideas service is shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.SetKeepAlivesEnabled(false)
		if err := s.Shutdown(ctx); err != nil {
			l.Fatalf("cannot gracefully shutdown: %v", err)
		}
		close(done)
	}()

	l.Printf("ideas service is serving on %s...", addr)
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		l.Fatalf("cannot listen on %s, err: %v\n", addr, err)
	}

	l.Println("goodbye!")
	<-done
}

func cors(next http.Handler) http.Handler {
	allowed := map[string]bool{
		"https://changkun.de":     true,
		"https://www.changkun.de": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ideas/ping" {
			next.ServeHTTP(w, r)
			return
		}

		// Try Bearer token from Authorization header.
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token := strings.TrimPrefix(h, "Bearer ")
			if _, err := login.Verify(token); err == nil {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Fall back to query param / cookie via SDK.
		if _, err := login.HandleAuth(w, r); err == nil {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				logger.Println(readIP(r), r.Method, r.URL.Path)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func readIP(r *http.Request) string {
	clientIP := r.Header.Get("X-Forwarded-For")
	clientIP = strings.TrimSpace(strings.Split(clientIP, ",")[0])
	if clientIP == "" {
		clientIP = strings.TrimSpace(r.Header.Get("X-Real-Ip"))
	}
	if clientIP != "" {
		return clientIP
	}
	ip, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return "unknown"
	}
	return ip
}
