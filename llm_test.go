package main

import (
	"encoding/json"
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
			name: "unescaped newlines in strings",
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
			name: "unescaped tabs in strings",
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
			name: "preserves already-escaped sequences",
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
			name: "mixed escaped and unescaped newlines",
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
			name: "escaped quotes inside strings preserved",
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
			name: "carriage return and newline",
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
