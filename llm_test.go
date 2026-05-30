package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepairJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  translateResult
	}{
		{
			name:  "already valid",
			input: `{"lang":"en","polished_title":"Title","polished_content":"Content","translated_title":"标题","translated_content":"内容"}`,
			want: translateResult{
				Lang:              "en",
				PolishedTitle:     "Title",
				PolishedContent:   "Content",
				TranslatedTitle:   "标题",
				TranslatedContent: "内容",
			},
		},
		{
			name:  "unescaped newlines in strings",
			input: "{\n  \"lang\": \"en\",\n  \"polished_title\": \"Title\",\n  \"polished_content\": \"Line one.\n\nLine two.\",\n  \"translated_title\": \"标题\",\n  \"translated_content\": \"第一行。\n\n第二行。\"\n}",
			want: translateResult{
				Lang:              "en",
				PolishedTitle:     "Title",
				PolishedContent:   "Line one.\n\nLine two.",
				TranslatedTitle:   "标题",
				TranslatedContent: "第一行。\n\n第二行。",
			},
		},
		{
			name:  "unescaped tabs in strings",
			input: "{\n  \"lang\": \"zh\",\n  \"polished_title\": \"标题\",\n  \"polished_content\": \"项目一\t项目二\",\n  \"translated_title\": \"Title\",\n  \"translated_content\": \"Item one\tItem two\"\n}",
			want: translateResult{
				Lang:              "zh",
				PolishedTitle:     "标题",
				PolishedContent:   "项目一\t项目二",
				TranslatedTitle:   "Title",
				TranslatedContent: "Item one\tItem two",
			},
		},
		{
			name:  "preserves already-escaped sequences",
			input: `{"lang":"en","polished_title":"Title","polished_content":"Line one.\n\nLine two.","translated_title":"标题","translated_content":"第一行。\n\n第二行。"}`,
			want: translateResult{
				Lang:              "en",
				PolishedTitle:     "Title",
				PolishedContent:   "Line one.\n\nLine two.",
				TranslatedTitle:   "标题",
				TranslatedContent: "第一行。\n\n第二行。",
			},
		},
		{
			name:  "mixed escaped and unescaped newlines",
			input: "{\n  \"lang\": \"en\",\n  \"polished_title\": \"Title\",\n  \"polished_content\": \"Para one.\\n\\nPara two.\nPara three.\",\n  \"translated_title\": \"标题\",\n  \"translated_content\": \"段落一。\\n\\n段落二。\n段落三。\"\n}",
			want: translateResult{
				Lang:              "en",
				PolishedTitle:     "Title",
				PolishedContent:   "Para one.\n\nPara two.\nPara three.",
				TranslatedTitle:   "标题",
				TranslatedContent: "段落一。\n\n段落二。\n段落三。",
			},
		},
		{
			name:  "escaped quotes inside strings preserved",
			input: `{"lang":"en","polished_title":"A \"Quoted\" Title","polished_content":"Content","translated_title":"「引用」标题","translated_content":"内容"}`,
			want: translateResult{
				Lang:              "en",
				PolishedTitle:     `A "Quoted" Title`,
				PolishedContent:   "Content",
				TranslatedTitle:   "「引用」标题",
				TranslatedContent: "内容",
			},
		},
		{
			name:  "carriage return and newline",
			input: "{\n  \"lang\": \"en\",\n  \"polished_title\": \"Title\",\n  \"polished_content\": \"Line one.\r\nLine two.\",\n  \"translated_title\": \"标题\",\n  \"translated_content\": \"行一。\r\n行二。\"\n}",
			want: translateResult{
				Lang:              "en",
				PolishedTitle:     "Title",
				PolishedContent:   "Line one.\r\nLine two.",
				TranslatedTitle:   "标题",
				TranslatedContent: "行一。\r\n行二。",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repaired := repairJSON(tt.input)

			var got translateResult
			if err := json.Unmarshal([]byte(repaired), &got); err != nil {
				t.Fatalf("repaired JSON still invalid: %v\nrepaired: %q", err, repaired)
			}
			if got != tt.want {
				t.Errorf("mismatch\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestLLMAPIFormatFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		baseURL string
		want    string
	}{
		{
			name:    "explicit openai wins",
			env:     "openai",
			baseURL: "https://lux.latere.ai/anthropic",
			want:    llmAPIFormatOpenAI,
		},
		{
			name:    "explicit anthropic",
			env:     "anthropic",
			baseURL: "https://lux.latere.ai/openrouter/v1",
			want:    llmAPIFormatAnthropic,
		},
		{
			name:    "lux anthropic base",
			baseURL: "https://lux.latere.ai/anthropic",
			want:    llmAPIFormatAnthropic,
		},
		{
			name:    "lux anthropic v1 base",
			baseURL: "https://lux.latere.ai/anthropic/v1",
			want:    llmAPIFormatAnthropic,
		},
		{
			name:    "openrouter base",
			baseURL: "https://lux.latere.ai/openrouter/v1",
			want:    llmAPIFormatOpenAI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := llmAPIFormatFromEnv(tt.env, tt.baseURL); got != tt.want {
				t.Fatalf("llmAPIFormatFromEnv(%q, %q) = %q, want %q", tt.env, tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestCompleteAnthropicUsesMessagesAPI(t *testing.T) {
	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer lux_test" {
			t.Fatalf("Authorization = %q, want bearer virtual key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
		}
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "claude-haiku-4-5-20251001" {
			t.Fatalf("model = %q, want stripped Anthropic model id", req.Model)
		}
		if req.System != "system" {
			t.Fatalf("system = %q, want system", req.System)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "hello" {
			t.Fatalf("messages = %#v, want one user message", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":" ok "}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := &llmClient{
		baseURL:   srv.URL,
		apiKey:    "lux_test",
		apiFormat: llmAPIFormatAnthropic,
		log:       log.New(io.Discard, "", 0),
	}
	got, err := c.complete(context.Background(), "anthropic/claude-haiku-4-5-20251001", "system", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("complete() = %q, want ok", got)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
}
