// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type llmClient struct {
	baseURL    string // e.g. "https://llm.changkun.de"
	apiKey     string
	model      string // e.g. "anthropic/claude-sonnet-4-5-20250929"
	titleModel string // e.g. "anthropic/claude-haiku-4-5-20251001"
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

const systemPrompt = `You are augmenting a short idea or note for a personal blog. Your task:
- Expand the idea with relevant context, research, and references
- Write in the same language as the original content
- Keep the augmented version concise but informative (roughly 2-4 paragraphs)
- Add relevant links or references where appropriate
- Do not repeat the original content, only expand on it
- Use markdown formatting`

func (c *llmClient) augment(ctx context.Context, title, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("Title: %s\n\nContent:\n%s", title, content)
	return c.complete(ctx, c.model, systemPrompt, prompt)
}

const titlePrompt = `Generate a short title (max 10 words) for the following idea/note.
Reply with ONLY the title text, no quotes, no punctuation at the end, no prefix.
Use the same language as the content.`

func (c *llmClient) generateTitle(ctx context.Context, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.complete(ctx, c.titleModel, titlePrompt, content)
}

func (c *llmClient) complete(ctx context.Context, model, system, user string) (string, error) {
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(c.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("LLM API error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM API")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}
