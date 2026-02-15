// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
)

type githubClient struct {
	token string
	owner string
	repo  string
	name  string
	email string
}

type createFileRequest struct {
	Message   string          `json:"message"`
	Content   string          `json:"content"` // base64-encoded
	Committer *githubCommiter `json:"committer,omitempty"`
}

type githubCommiter struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (g *githubClient) createFile(ctx context.Context, path, content, commitMsg string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reqBody := createFileRequest{
		Message: commitMsg,
		Content: base64.StdEncoding.EncodeToString([]byte(content)),
		Committer: &githubCommiter{
			Name:  g.name,
			Email: g.email,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s",
		g.owner, g.repo, path)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// sanitizeCommitMsg strips control characters and truncates the message.
func sanitizeCommitMsg(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	s = strings.TrimSpace(b.String())
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
