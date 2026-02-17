package main

import "testing"

func TestDetectLang(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "english text",
			input: "This is a simple English sentence about programming.",
			want:  "en",
		},
		{
			name:  "chinese text",
			input: "这是一个关于编程的简单中文句子。",
			want:  "zh",
		},
		{
			name:  "mostly english with some chinese",
			input: "This is mostly English with a few 中文 words mixed in for testing purposes.",
			want:  "en",
		},
		{
			name:  "mostly chinese with some english",
			input: "这主要是中文内容，只有少量English单词混在其中。",
			want:  "zh",
		},
		{
			name:  "empty string",
			input: "",
			want:  "en",
		},
		{
			name:  "only punctuation and spaces",
			input: "   ... !!! ???   ",
			want:  "en",
		},
		{
			name:  "mixed markdown with english",
			input: "## Title\n\nSome **bold** text and [a link](https://example.com).",
			want:  "en",
		},
		{
			name:  "mixed markdown with chinese",
			input: "## 标题\n\n这是一段包含**加粗**文本的中文内容，用于测试语言检测功能。",
			want:  "zh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectLang(tt.input)
			if got != tt.want {
				t.Errorf("detectLang(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
