package prices

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func find(ps []Price, model string) (Price, bool) {
	for _, p := range ps {
		if p.Model == model {
			return p, true
		}
	}
	return Price{}, false
}

func TestParseOpenAI(t *testing.T) {
	ps := ParseOpenAI(read(t, "openai-pricing.md"))
	if len(ps) < 20 {
		t.Fatalf("only %d models parsed", len(ps))
	}
	p, ok := find(ps, "gpt-4o-mini")
	if !ok || p.Input != 0.15 || p.Output != 0.60 || p.CachedInput != 0.075 {
		t.Fatalf("gpt-4o-mini = %+v", p)
	}
	// Short-context rate and the 30m cache write, not batch or long-context.
	p, ok = find(ps, "gpt-6-astra")
	if !ok || p.Input != 10 || p.Output != 50 || p.CacheWrite != 12.5 {
		t.Fatalf("gpt-6-astra = %+v", p)
	}
	p, ok = find(ps, "gpt-5.5")
	if !ok || p.Input != 5 || p.Note == "" {
		t.Fatalf("gpt-5.5 = %+v", p)
	}
	p, ok = find(ps, "text-embedding-3-small")
	if !ok || p.Input != 0.02 || p.Output != 0 {
		t.Fatalf("text-embedding-3-small = %+v", p)
	}
	if p, ok = find(ps, "omni-moderation-latest"); !ok || p.Input != 0 || p.Note != "free" {
		t.Fatalf("moderation = %+v", p)
	}
	if p, ok = find(ps, "gpt-5.3-codex"); !ok || p.Input != 1.75 {
		t.Fatalf("codex should keep the standard rate, not fast: %+v", p)
	}
	for _, p := range ps {
		if p.Provider != "openai" || p.Model == "" {
			t.Fatalf("bad row %+v", p)
		}
	}
}

func TestParseAnthropic(t *testing.T) {
	ps := ParseAnthropic(read(t, "anthropic-pricing.md"))
	if len(ps) < 10 {
		t.Fatalf("only %d models parsed", len(ps))
	}
	p, ok := find(ps, "claude-opus-4-5")
	if !ok || p.Input != 5 || p.Output != 25 || p.CachedInput != 0.5 || p.CacheWrite != 6.25 || p.CacheWrite1h != 10 {
		t.Fatalf("claude-opus-4-5 = %+v", p)
	}
	if _, ok := find(ps, "claude-opus-4-1"); !ok {
		t.Fatal("retired model with a linked note was dropped")
	}
	if _, ok := find(ps, "claude-3-5-haiku"); !ok {
		t.Fatal("3.x alias missing")
	}
	p, _ = find(ps, "claude-haiku-5-5")
	if p.Input != 0.10 || p.Note == "" {
		t.Fatalf("first tier should win with a note: %+v", p)
	}
}

func TestLookupAndCost(t *testing.T) {
	tab := &Table{}
	tab.Replace("openai", "x", time.Now(), []Price{
		{Provider: "openai", Model: "gpt-4o", Input: 2.5, CachedInput: 1.25, Output: 10},
		{Provider: "openai", Model: "gpt-4o-mini", Input: 0.15, Output: 0.6},
	})
	tab.Replace("anthropic", "y", time.Now(), []Price{
		{Provider: "anthropic", Model: "claude-sonnet-4-5", Input: 3, CachedInput: 0.3, CacheWrite: 3.75, CacheWrite1h: 6, Output: 15},
	})
	if p, ok := tab.Lookup("openai", "gpt-4o-mini-2024-07-18"); !ok || p.Model != "gpt-4o-mini" {
		t.Fatalf("lookup = %+v %v", p, ok)
	}
	if p, ok := tab.Lookup("openai", "gpt-4o-2024-08-06"); !ok || p.Model != "gpt-4o" {
		t.Fatalf("lookup = %+v %v", p, ok)
	}
	if _, ok := tab.Lookup("openai", "gpt-4"); ok {
		t.Fatal("gpt-4 must not match gpt-4o")
	}
	p, ok := tab.Lookup("anthropic", "claude-sonnet-4-5-20250929")
	if !ok {
		t.Fatal("dated anthropic id")
	}
	// 1M uncached + 1M read + 1M 5m-write + 1M 1h-write + 1M out
	got := p.Cost(1e6, 1e6, 1e6, 1e6, 1e6)
	if math.Abs(got-(3+0.3+3.75+6+15)) > 1e-9 {
		t.Fatalf("cost = %v", got)
	}
	// Replace keeps the other provider.
	tab.Replace("openai", "x", time.Now(), nil)
	if _, ok := tab.Lookup("anthropic", "claude-sonnet-4-5"); !ok || len(tab.Prices) != 1 {
		t.Fatalf("replace dropped the other provider: %+v", tab.Prices)
	}
}

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	tab, used, err := Load(path)
	if err != nil || used || tab == nil {
		t.Fatalf("default: %v %v", used, err)
	}
	tab.Replace("openai", "u", time.Unix(0, 0), []Price{{Provider: "openai", Model: "m", Input: 1, Output: 2}})
	if err := Save(path, tab); err != nil {
		t.Fatal(err)
	}
	got, used, err := Load(path)
	if err != nil || !used {
		t.Fatalf("load: %v %v", used, err)
	}
	if p, ok := got.Lookup("openai", "m"); !ok || p.Output != 2 {
		t.Fatalf("round trip: %+v", got)
	}
}
