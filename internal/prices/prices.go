// Package prices is the local per-model price table: an embedded snapshot
// of the providers' public pricing pages, replaced by
// ~/.llmspend/prices.json once 'llmspend prices refresh' has run.
package prices

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Pricing pages, fetched as Markdown.
const (
	OpenAIURL    = "https://developers.openai.com/api/docs/pricing.md"
	AnthropicURL = "https://platform.claude.com/docs/en/about-claude/pricing.md"
)

// Price is a model's standard-tier rate in USD per million tokens.
// CacheWrite is OpenAI's 30-minute write or Anthropic's 5-minute write;
// CacheWrite1h is Anthropic's 1-hour write. Zero means "not listed".
type Price struct {
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Input        float64 `json:"input"`
	CachedInput  float64 `json:"cached_input,omitempty"`
	CacheWrite   float64 `json:"cache_write,omitempty"`
	CacheWrite1h float64 `json:"cache_write_1h,omitempty"`
	Output       float64 `json:"output"`
	Note         string  `json:"note,omitempty"`
}

// Table is the whole price list plus where and when it came from.
type Table struct {
	FetchedAt map[string]time.Time `json:"fetched_at"` // provider → time
	Sources   map[string]string    `json:"sources"`    // provider → URL
	Prices    []Price              `json:"prices"`
}

//go:embed default.json
var defaultJSON []byte

// Default is the snapshot compiled into the binary.
func Default() (*Table, error) {
	var t Table
	if err := json.Unmarshal(defaultJSON, &t); err != nil {
		return nil, fmt.Errorf("embedded price table: %w", err)
	}
	return &t, nil
}

// Load returns the refreshed table at path when it exists, else the default.
// The bool reports whether the file was used.
func Load(path string) (*Table, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t, err := Default()
		return t, false, err
	}
	if err != nil {
		return nil, false, err
	}
	var t Table
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w (delete it to fall back to the built-in table)", path, err)
	}
	return &t, true, nil
}

// Save writes the table as indented JSON.
func Save(path string, t *Table) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Replace swaps one provider's rows for fresh ones, keeping the others.
func (t *Table) Replace(provider, source string, at time.Time, ps []Price) {
	keep := t.Prices[:0:0]
	for _, p := range t.Prices {
		if p.Provider != provider {
			keep = append(keep, p)
		}
	}
	t.Prices = append(keep, ps...)
	sort.SliceStable(t.Prices, func(i, j int) bool { return t.Prices[i].Provider < t.Prices[j].Provider })
	if t.FetchedAt == nil {
		t.FetchedAt = map[string]time.Time{}
	}
	if t.Sources == nil {
		t.Sources = map[string]string{}
	}
	t.FetchedAt[provider] = at.UTC()
	t.Sources[provider] = source
}

// Lookup finds the price for a model id as the usage APIs report it:
// exact match first, then the longest listed model that is a prefix
// ending at a "-" boundary ("gpt-4o-mini-2024-07-18" → "gpt-4o-mini",
// "claude-sonnet-4-5-20250929" → "claude-sonnet-4-5").
func (t *Table) Lookup(provider, model string) (Price, bool) {
	model = strings.ToLower(model)
	var best Price
	found := false
	for _, p := range t.Prices {
		if p.Provider != provider {
			continue
		}
		m := strings.ToLower(p.Model)
		if m == model {
			return p, true
		}
		if strings.HasPrefix(model, m+"-") && (!found || len(m) > len(best.Model)) {
			best, found = p, true
		}
	}
	return best, found
}

// Cost estimates USD for token counts at this price. uncached input, cache
// reads, cache writes (5m/30m and 1h) and output are billed separately.
func (p Price) Cost(uncached, cacheRead, cacheWrite, cacheWrite1h, output int64) float64 {
	cached := p.CachedInput
	if cached == 0 {
		cached = p.Input
	}
	write := p.CacheWrite
	if write == 0 {
		write = p.Input
	}
	write1h := p.CacheWrite1h
	if write1h == 0 {
		write1h = write
	}
	return (float64(uncached)*p.Input +
		float64(cacheRead)*cached +
		float64(cacheWrite)*write +
		float64(cacheWrite1h)*write1h +
		float64(output)*p.Output) / 1e6
}
