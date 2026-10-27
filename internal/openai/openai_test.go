package openai

import "testing"

func TestModelOfLineItem(t *testing.T) {
	for in, want := range map[string]string{
		"gpt-4o-mini-2024-07-18, input":          "gpt-4o-mini-2024-07-18",
		"gpt-4o, cached input":                   "gpt-4o",
		"web search tool calls":                  "web search tool calls",
		"flex | gpt-6-luna, output":              "gpt-6-luna",
		"gpt-image-2.5-flare image, output":      "gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst text, input":     "gpt-image-2.5-sunburst",
		"flex | gpt-6-luna, cache writes - 12hr": "gpt-6-luna",
		"whisper":                                "whisper",
		"":                                       "",
	} {
		if got := ModelOfLineItem(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestShortHint(t *testing.T) {
	if got := shortHint("sk-proj-********5RsA"); got != "sk-proj-…5RsA" {
		t.Fatal(got)
	}
	if got := shortHint("sk-abc...def"); got != "sk-abc...def" {
		t.Fatal(got)
	}
}
