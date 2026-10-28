package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/iamnikolie/llmspend/internal/prices"
	"github.com/spf13/cobra"
)

func pricesPath() (string, error) {
	h, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "prices.json"), nil
}

func loadPrices() (*prices.Table, error) {
	p, err := pricesPath()
	if err != nil {
		return nil, err
	}
	t, _, err := prices.Load(p)
	return t, err
}

var pricesCmd = &cobra.Command{
	Use:     "prices [model]",
	Aliases: []string{"models"},
	Short:   "The local price table (USD per 1M tokens, Gemini music per song); filter by a model substring",
	Example: `  llmspend prices
  llmspend prices -p anthropic
  llmspend prices gpt-4o
  llmspend prices refresh`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := pricesPath()
		if err != nil {
			return err
		}
		t, fromFile, err := prices.Load(path)
		if err != nil {
			return err
		}
		filter := ""
		if len(args) == 1 {
			filter = strings.ToLower(args[0])
		}
		var rows []map[string]any
		for _, p := range t.Prices {
			if providerFlag != "" && p.Provider != providerFlag {
				continue
			}
			if filter != "" && !strings.Contains(p.Model, filter) {
				continue
			}
			row := map[string]any{"provider": p.Provider, "model": p.Model, "input": p.Input, "output": p.Output}
			if p.CachedInput > 0 {
				row["cached_input"] = p.CachedInput
			}
			if p.CacheWrite > 0 {
				row["cache_write"] = p.CacheWrite
			}
			if p.CacheWrite1h > 0 {
				row["cache_write_1h"] = p.CacheWrite1h
			}
			for k, v := range map[string]float64{"output_image": p.OutputImage, "output_audio": p.OutputAudio, "output_video": p.OutputVideo, "per_request": p.PerRequest} {
				if v > 0 {
					row[k] = v
				}
			}
			if p.Note != "" {
				row["note"] = p.Note
			}
			rows = append(rows, row)
		}
		cols := []string{"provider", "model", "input", "cached_input", "cache_write", "cache_write_1h", "output"}
		// Gemini's per-modality and per-request columns only when a row has them.
		for _, c := range []string{"output_image", "output_audio", "output_video", "per_request"} {
			for _, r := range rows {
				if _, ok := r[c]; ok {
					cols = append(cols, c)
					break
				}
			}
		}
		if err := emitRows(rows, append(cols, "note")); err != nil {
			return err
		}
		src := "built-in snapshot"
		if fromFile {
			src = path
		}
		var when []string
		for _, prov := range []string{config.OpenAI, config.Anthropic, config.Gemini} {
			if at, ok := t.FetchedAt[prov]; ok {
				when = append(when, prov+" "+day(at))
			}
		}
		footer("Standard-tier USD per 1M tokens (per_request: USD per request). Source: %s (fetched %s). cache_write is OpenAI's 30m or Anthropic's 5m write; output_image/audio/video are Gemini's output rates for those modalities.", src, strings.Join(when, ", "))
		return nil
	},
}

var refreshOut string

var pricesRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-read the providers' public pricing pages into ~/.llmspend/prices.json",
	Long: "Fetches OpenAI's, Anthropic's and Google's Gemini API pricing pages (public Markdown,\n" +
		"no key needed), parses the standard-tier prices and writes ~/.llmspend/prices.json,\n" +
		"which then replaces the built-in snapshot. -p refreshes one provider and keeps the\n" +
		"others. Gemini also reads the deprecations page: a retired id the API routes to a\n" +
		"newer model is priced as that model.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := refreshOut
		if path == "" {
			var err error
			if path, err = pricesPath(); err != nil {
				return err
			}
		}
		t, _, err := prices.Load(path)
		if err != nil {
			return err
		}
		old := map[string]prices.Price{}
		for _, p := range t.Prices {
			old[p.Provider+"/"+p.Model] = p
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()
		page := func(url string, parse func(string) []prices.Price) func() ([]prices.Price, error) {
			return func() ([]prices.Price, error) {
				md, err := fetchPage(ctx, url)
				if err != nil {
					return nil, err
				}
				return parse(md), nil
			}
		}
		sources := []struct {
			provider, url string
			read          func() ([]prices.Price, error)
		}{
			{config.OpenAI, prices.OpenAIURL, page(prices.OpenAIURL, prices.ParseOpenAI)},
			{config.Anthropic, prices.AnthropicURL, page(prices.AnthropicURL, prices.ParseAnthropic)},
			{config.Gemini, prices.GeminiURL, func() ([]prices.Price, error) {
				ps, err := page(prices.GeminiURL, prices.ParseGemini)()
				if err != nil || len(ps) == 0 {
					return ps, err
				}
				md, err := fetchPage(ctx, prices.GeminiDeprecationsURL)
				if err != nil {
					fmt.Fprintf(stderr, "note: gemini deprecations page not read (%v); retired model ids stay unpriced\n", err)
					return ps, nil
				}
				return prices.GeminiRouted(ps, md), nil
			}},
		}
		var rows []map[string]any
		for _, s := range sources {
			if providerFlag != "" && providerFlag != s.provider {
				continue
			}
			ps, err := s.read()
			if err != nil {
				return fmt.Errorf("%s pricing page: %w", s.provider, err)
			}
			if len(ps) == 0 {
				return fmt.Errorf("%s pricing page: no prices found at %s; the page layout may have changed, table left as it was", s.provider, s.url)
			}
			added, changed := 0, 0
			seen := map[string]bool{}
			for _, p := range ps {
				k := p.Provider + "/" + p.Model
				seen[k] = true
				if o, ok := old[k]; !ok {
					added++
				} else if o != p {
					changed++
				}
			}
			removed := 0
			for k := range old {
				if strings.HasPrefix(k, s.provider+"/") && !seen[k] {
					removed++
				}
			}
			t.Replace(s.provider, s.url, now(), ps)
			rows = append(rows, map[string]any{"provider": s.provider, "models": len(ps), "added": added, "changed": changed, "removed": removed, "source": s.url})
		}
		sort.SliceStable(t.Prices, func(i, j int) bool { return t.Prices[i].Provider < t.Prices[j].Provider })
		if err := prices.Save(path, t); err != nil {
			return err
		}
		if err := emitRows(rows, []string{"provider", "models", "added", "changed", "removed", "source"}); err != nil {
			return err
		}
		footer("Wrote %s", path)
		return nil
	},
}

func fetchPage(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "llmspend/"+version)
	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9, */*;q=0.1")
	if verbose {
		fmt.Fprintf(stderr, "→ GET %s\n", url)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(b), nil
}

func init() {
	pricesRefreshCmd.Flags().StringVar(&refreshOut, "out", "", "write here instead of ~/.llmspend/prices.json")
	_ = pricesRefreshCmd.Flags().MarkHidden("out")
	pricesCmd.AddCommand(pricesRefreshCmd)
	rootCmd.AddCommand(pricesCmd)
}
