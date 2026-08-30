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
	"log"
	"net/http"
	"strings"
	"time"

	"latere.ai/x/pkg/sanitize"
)

type llmClient struct {
	baseURL    string // e.g. "https://llm.changkun.de"
	apiKey     string
	model      string // e.g. "anthropic/claude-sonnet-4-5-20250929"
	titleModel string // e.g. "anthropic/claude-haiku-4-5-20251001"
	apiFormat  string // "openai" or "anthropic"; empty defaults to openai-compatible
	log        *log.Logger
}

const (
	llmAPIFormatOpenAI    = "openai"
	llmAPIFormatAnthropic = "anthropic"
)

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequestWithOptions struct {
	Model            string            `json:"model"`
	Messages         []chatMessage     `json:"messages"`
	WebSearchOptions *webSearchOptions `json:"web_search_options,omitempty"`
	Tools            []tool            `json:"tools,omitempty"`
}

type webSearchOptions struct {
	SearchContextSize string `json:"search_context_size"`
}

type tool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses,omitempty"`
}

type completionOptions struct {
	WebSearchOptions *webSearchOptions
	Tools            []tool
}

type anthropicRequest struct {
	Model     string        `json:"model"`
	System    string        `json:"system,omitempty"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicResponse struct {
	Content []contentBlock `json:"content"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func llmAPIFormatFromEnv(value, baseURL string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case llmAPIFormatOpenAI, "openai-compatible", "chat-completions":
		return llmAPIFormatOpenAI
	case llmAPIFormatAnthropic, "claude", "messages":
		return llmAPIFormatAnthropic
	case "":
		// Autodetect below.
	default:
		return llmAPIFormatOpenAI
	}

	trimmed := strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
	if strings.HasSuffix(trimmed, "/anthropic") || strings.HasSuffix(trimmed, "/anthropic/v1") {
		return llmAPIFormatAnthropic
	}
	return llmAPIFormatOpenAI
}

func (c *llmClient) format() string {
	if c.apiFormat == llmAPIFormatAnthropic {
		return llmAPIFormatAnthropic
	}
	return llmAPIFormatOpenAI
}

func (c *llmClient) supportsCompletionOptions() bool {
	return c.format() == llmAPIFormatOpenAI
}

func (c *llmClient) modelForRequest(model string) string {
	if c.format() == llmAPIFormatAnthropic {
		return strings.TrimPrefix(model, "anthropic/")
	}
	return model
}

func (c *llmClient) augment(ctx context.Context, sourceLang, title, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	prompt, err := renderPrompt(promptIdeaInput, ideaInputData{Title: title, Content: content})
	if err != nil {
		return "", err
	}

	// Try with web search + web fetch for grounded citations.
	if c.supportsCompletionOptions() {
		sysPrompt, err := renderPrompt(promptAugment, augmentData{Lang: sourceLang, WebTools: true})
		if err != nil {
			return "", err
		}
		result, err := c.completeWithOptions(ctx, c.model, sysPrompt, prompt, &completionOptions{
			WebSearchOptions: &webSearchOptions{SearchContextSize: "medium"},
			Tools: []tool{
				{Type: "web_fetch_20250910", Name: "web_fetch", MaxUses: 5},
			},
		})
		if err == nil {
			return result, nil
		}
		c.log.Printf("augment with web search failed, falling back to plain: %v", err)
	}

	sysPrompt, err := renderPrompt(promptAugment, augmentData{Lang: sourceLang})
	if err != nil {
		return "", err
	}
	return c.complete(ctx, c.model, sysPrompt, prompt)
}

func (c *llmClient) generateTitle(ctx context.Context, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	system, err := renderPrompt(promptTitle, nil)
	if err != nil {
		return "", err
	}
	return c.complete(ctx, c.titleModel, system, content)
}

func (c *llmClient) improveContent(ctx context.Context, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	system, err := renderPrompt(promptImprove, nil)
	if err != nil {
		return "", err
	}
	return c.complete(ctx, c.titleModel, system, content)
}

type translateResult struct {
	Lang              string `json:"lang"`
	PolishedTitle     string `json:"polished_title"`
	PolishedContent   string `json:"polished_content"`
	TranslatedTitle   string `json:"translated_title"`
	TranslatedContent string `json:"translated_content"`
}

func (c *llmClient) detectAndTranslate(ctx context.Context, title, content string) (*translateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	system, err := renderPrompt(promptDetectTranslate, nil)
	if err != nil {
		return nil, err
	}
	prompt, err := renderPrompt(promptIdeaInput, ideaInputData{Title: title, Content: content})
	if err != nil {
		return nil, err
	}
	raw, err := c.complete(ctx, c.titleModel, system, prompt)
	if err != nil {
		return nil, err
	}

	// Strip markdown code fences if present.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result translateResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// LLMs sometimes produce JSON with unescaped control characters
		// inside string values. Try to repair before giving up.
		repaired := repairJSON(raw)
		if err2 := json.Unmarshal([]byte(repaired), &result); err2 != nil {
			return nil, fmt.Errorf("parse translation response: %w (raw: %s)", err, raw)
		}
	}
	if result.Lang != "en" && result.Lang != "zh" {
		return nil, fmt.Errorf("unexpected language: %q", result.Lang)
	}
	return &result, nil
}

func (c *llmClient) generateSlug(ctx context.Context, titleEn string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	system, err := renderPrompt(promptSlug, nil)
	if err != nil {
		return "", err
	}
	raw, err := c.complete(ctx, c.titleModel, system, titleEn)
	if err != nil {
		return "", err
	}

	// sanitize.Slug substitutes a fixed placeholder when the input carries
	// nothing alphanumeric. That is a sensible default for a container name
	// and a bad one for a permalink, so the unusable case is rejected here
	// instead: a post must never publish under a slug the model did not
	// produce. Checking the input rather than comparing against the
	// placeholder also keeps a model that legitimately answers "task" from
	// being mistaken for a failure.
	if !hasSlugChar(raw) {
		return "", fmt.Errorf("no usable slug in model output %q", raw)
	}
	return sanitize.Slug(raw, slugMaxLen), nil
}

// slugMaxLen bounds the permalink. sanitize.Slug stops accumulating once the
// result reaches this length, so the bound is exact.
const slugMaxLen = 60

// hasSlugChar reports whether s carries a character sanitize.Slug will keep.
// It mirrors that function's own test, applied after the same lowercasing, so
// the two cannot disagree about what counts as empty.
func hasSlugChar(s string) bool {
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func (c *llmClient) translateTitle(ctx context.Context, title, targetLang string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	system, err := renderPrompt(promptTranslateTitle, languageData{Language: languageName(targetLang)})
	if err != nil {
		return "", err
	}
	return c.complete(ctx, c.titleModel, system, title)
}

func (c *llmClient) translateContent(ctx context.Context, content, targetLang string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	system, err := renderPrompt(promptTranslateContent, languageData{Language: languageName(targetLang)})
	if err != nil {
		return "", err
	}
	return c.complete(ctx, c.titleModel, system, content)
}

func (c *llmClient) complete(ctx context.Context, model, system, user string) (string, error) {
	return c.completeWithOptions(ctx, model, system, user, nil)
}

func (c *llmClient) completeWithOptions(ctx context.Context, model, system, user string, opts *completionOptions) (string, error) {
	model = c.modelForRequest(model)
	if c.format() == llmAPIFormatAnthropic {
		if opts != nil {
			return "", fmt.Errorf("completion options are not supported by the Anthropic Messages API")
		}
		return c.completeAnthropic(ctx, model, system, user)
	}

	messages := []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	var body []byte
	var err error
	if opts != nil {
		body, err = json.Marshal(chatRequestWithOptions{
			Model:            model,
			Messages:         messages,
			WebSearchOptions: opts.WebSearchOptions,
			Tools:            opts.Tools,
		})
	} else {
		body, err = json.Marshal(chatRequest{
			Model:    model,
			Messages: messages,
		})
	}
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(c.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

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

	return c.extractContent(result.Choices[0].Message.Content), nil
}

const anthropicDefaultMaxTokens = 4096

func (c *llmClient) completeAnthropic(ctx context.Context, model, system, user string) (string, error) {
	body, err := json.Marshal(anthropicRequest{
		Model:     model,
		System:    system,
		Messages:  []chatMessage{{Role: "user", Content: user}},
		MaxTokens: anthropicDefaultMaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicMessagesURL(c.baseURL), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

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

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("LLM API error: %s", result.Error.Message)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from LLM API")
	}
	raw, err := json.Marshal(result.Content)
	if err != nil {
		return "", fmt.Errorf("marshal response content: %w", err)
	}
	return c.extractContent(raw), nil
}

func anthropicMessagesURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func (c *llmClient) setAuthHeaders(req *http.Request) {
	if c.format() == llmAPIFormatAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		if strings.HasPrefix(strings.TrimSpace(c.apiKey), "lux_") {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
			return
		}
		req.Header.Set("x-api-key", c.apiKey)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// extractContent handles both plain string and array-of-blocks content.
// When the response contains content blocks (e.g. from server-side tool use),
// it concatenates the text blocks and logs non-text blocks.
func (c *llmClient) extractContent(raw json.RawMessage) string {
	// Try plain string first (standard OpenAI format).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}

	// Try array of content blocks (Anthropic format passed through).
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		c.log.Printf("unexpected content format: %s", string(raw))
		return strings.TrimSpace(string(raw))
	}

	// When the response contains tool-use blocks (e.g. from web search),
	// intermediate text blocks are the LLM "thinking aloud" between tool
	// calls ("I need to search for...", "Let me verify..."). Only the
	// final text block contains the actual structured response.
	hasToolUse := false
	for _, b := range blocks {
		if b.Type != "text" {
			hasToolUse = true
			break
		}
	}

	if hasToolUse {
		// Return only the last text block (the final answer).
		var lastText string
		for _, b := range blocks {
			if b.Type == "text" {
				lastText = b.Text
			}
		}
		return strings.TrimSpace(lastText)
	}

	// No tool use: concatenate all text blocks as before.
	var text strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	return strings.TrimSpace(text.String())
}

// repairJSON escapes unescaped control characters (newlines, tabs, etc.)
// inside JSON string values. LLMs sometimes produce pretty-printed JSON
// with literal newlines in string content instead of \n escape sequences.
func repairJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch c {
			case '"':
				inString = false
				b.WriteByte(c)
			case '\\':
				// Keep existing escape sequences as-is.
				b.WriteByte(c)
				if i+1 < len(s) {
					i++
					b.WriteByte(s[i])
				}
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteByte(c)
			}
		} else {
			if c == '"' {
				inString = true
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
