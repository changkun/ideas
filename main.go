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
)

func main() {
	l := log.New(os.Stdout, "ideas: ", log.LstdFlags|log.Lshortfile|log.Lmsgprefix)

	token := os.Getenv("IDEAS_TOKEN")
	if token == "" {
		l.Fatal("IDEAS_TOKEN is required")
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
		log:   l,
		token: token,
		llm: &llmClient{
			baseURL:    llmBaseURL,
			apiKey:     llmAPIKey,
			model:      cmp.Or(os.Getenv("LLM_MODEL"), "anthropic/claude-sonnet-4-5-20250929"),
			titleModel: cmp.Or(os.Getenv("LLM_TITLE_MODEL"), "anthropic/claude-haiku-4-5-20251001"),
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

	addr := cmp.Or(os.Getenv("IDEAS_ADDR"), "0.0.0.0:80")
	s := &http.Server{
		Addr:         addr,
		Handler:      logging(l)(auth(token)(r)),
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

func auth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ideas/ping" {
				next.ServeHTTP(w, r)
				return
			}
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") || strings.TrimPrefix(h, "Bearer ") != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
