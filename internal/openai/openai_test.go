package openai

import "testing"

func TestModelOfLineItem(t *testing.T) {
	for in, want := range map[string]string{
		"gpt-4o-mini-2024-07-18, input": "gpt-4o-mini-2024-07-18",
		"gpt-4o, cached input":          "gpt-4o",
		"web search tool calls":         "web search tool calls",
		"":                              "",
	} {
		if got := ModelOfLineItem(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
