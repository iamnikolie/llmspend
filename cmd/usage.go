package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/iamnikolie/llmspend/internal/client"
	"github.com/iamnikolie/llmspend/internal/openai"
	"github.com/iamnikolie/llmspend/internal/prices"
	"github.com/iamnikolie/llmspend/internal/spend"
	"github.com/spf13/cobra"
)

var (
	usageKinds  []string
	usageSince  string
	usageUntil  string
	usageBy     []string
	usageBucket string
)

var usageDims = map[string]bool{"model": true, "key": true, "project": true, "day": true}

func checkDims(by []string, allowed map[string]bool) (map[string]bool, error) {
	out := map[string]bool{}
	for _, b := range by {
		b = strings.ToLower(strings.TrimSpace(b))
		switch b {
		case "":
			continue
		case "models":
			b = "model"
		case "keys", "api_key", "api-key":
			b = "key"
		case "projects", "workspace", "workspaces":
			b = "project"
		case "date", "days", "time", "hour":
			b = "day"
		}
		if !allowed[b] {
			keys := make([]string, 0, len(allowed))
			for k := range allowed {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return nil, fmt.Errorf("--by %q: use %s (comma-separated to combine)", b, strings.Join(keys, ", "))
		}
		out[b] = true
	}
	return out, nil
}

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Usage of every kind per model, key, project or day, with estimated cost",
	Long: "Usage from every OpenAI Usage API family (completions, embeddings, moderations,\n" +
		"decisions, images, speech, transcription, web_search, file_search,\n" +
		"code_interpreter, vector_stores) and Anthropic's Messages usage report (kind\n" +
		"messages). Token kinds show tokens; the others show a quantity and its unit.\n" +
		"est_usd prices token kinds with the local table ('llmspend prices'); the billed\n" +
		"amount, every kind included, is 'llmspend cost'.",
	Example: `  llmspend usage                               # last 7 days, per model, every org
  llmspend usage --since 30d --by key          # which key burned what
  llmspend usage -p openai --by project,model
  llmspend usage --kind embeddings --since 30d --by key
  llmspend usage --since 24h --bucket 1h --by day`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dims, err := checkDims(usageBy, usageDims)
		if err != nil {
			return err
		}
		if usageBucket != "1d" && usageBucket != "1h" {
			return fmt.Errorf("--bucket must be 1d or 1h")
		}
		from, err := parseSince(usageSince)
		if err != nil {
			return err
		}
		to, err := parseUntil(usageUntil)
		if err != nil {
			return err
		}
		kinds, err := selectKinds(usageKinds)
		if err != nil {
			return err
		}
		ts, err := targets("")
		if err != nil {
			return err
		}
		table, err := loadPrices()
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()

		agg := &usageAgg{dims: dims, bucket: usageBucket, prices: table, rows: map[string]*usageRow{}, unpriced: map[string]bool{}}
		err = eachOrg(ts, func(t target) error {
			us, err := fetchUsage(ctx, t, kinds, from, to, usageBucket, dims)
			if err != nil {
				return err
			}
			n := resolveNames(ctx, t, dims["project"], dims["key"])
			agg.add(t, n, us)
			return nil
		})
		if err != nil {
			return err
		}
		return agg.emit()
	},
}

// anthropicKind is the one usage family Anthropic reports.
const anthropicKind = "messages"

// selectKinds reads --kind; empty means every kind of every provider.
func selectKinds(in []string) (map[string]bool, error) {
	all := map[string]bool{anthropicKind: true}
	for _, k := range openai.Kinds {
		all[k.Name] = true
	}
	if len(in) == 0 {
		return all, nil
	}
	aliases := map[string]string{
		"chat": "completions", "responses": "completions", "completion": "completions",
		"embedding": "embeddings", "moderation": "moderations", "image": "images",
		"audio_speeches": "speech", "tts": "speech", "audio_transcriptions": "transcription",
		"stt": "transcription", "web_search_calls": "web_search", "file_search_calls": "file_search",
		"code_interpreter_sessions": "code_interpreter", "vector_store": "vector_stores",
		"message": "messages", "decision": "decisions",
	}
	out := map[string]bool{}
	for _, k := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		if a, ok := aliases[k]; ok {
			k = a
		}
		if k == "" {
			continue
		}
		if !all[k] {
			names := make([]string, 0, len(all))
			for n := range all {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("--kind %q: use %s", k, strings.Join(names, ", "))
		}
		out[k] = true
	}
	return out, nil
}

// fetchUsage reads every selected kind. It always groups by model at the
// provider so token rows can be priced; the other dimensions are added when
// asked for. A kind the org cannot read is a stderr note; a rejected key
// stops the org at the first call.
func fetchUsage(ctx context.Context, t target, kinds map[string]bool, from, to time.Time, bucket string, dims map[string]bool) ([]spend.Usage, error) {
	group := []string{"model"}
	if dims["key"] {
		group = append(group, "api_key_id")
	}
	if t.an != nil {
		if !kinds[anthropicKind] {
			return nil, nil
		}
		if dims["project"] {
			group = append(group, "workspace_id")
		}
		return t.an.Usage(ctx, from, to, bucket, group)
	}
	if dims["project"] {
		group = append(group, "project_id")
	}
	var out []spend.Usage
	read := 0
	var lastErr error
	for _, k := range openai.Kinds {
		if !kinds[k.Name] {
			continue
		}
		us, err := t.oa.Usage(ctx, k, from, to, bucket, group)
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
			return nil, err
		}
		if err != nil {
			fmt.Fprintf(stderr, "! %s: %s usage not read: %v\n", t.org.Name, k.Name, err)
			lastErr = err
			continue
		}
		read++
		out = append(out, us...)
	}
	if read == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

type usageRow struct {
	date, provider, org, kind, project, key, model, unit string
	requests, input, cached, write, output, quantity     int64
	est                                                  float64
	priced                                               bool
}

type usageAgg struct {
	dims     map[string]bool
	bucket   string
	prices   *prices.Table
	rows     map[string]*usageRow
	order    []string
	unpriced map[string]bool
	hasOA    bool
	hasQty   bool
}

// estimate prices token usage; kinds counted in other units are not priced
// here (their billed amount is in 'cost').
func estimate(tab *prices.Table, provider string, u spend.Usage) (float64, bool) {
	if u.Unit != "" {
		return 0, false
	}
	p, ok := tab.Lookup(provider, u.Model)
	if !ok {
		return 0, false
	}
	uncached := u.Input - u.CachedInput - u.CacheWrite
	if uncached < 0 {
		uncached = 0
	}
	return p.Cost(uncached, u.CachedInput, u.CacheWrite-u.CacheWrite1h, u.CacheWrite1h, u.Output), true
}

func (a *usageAgg) add(t target, n names, us []spend.Usage) {
	if t.oa != nil {
		a.hasOA = true
	}
	for _, u := range us {
		r := usageRow{provider: t.org.Provider, org: t.org.Name, kind: u.Kind, unit: u.Unit}
		if a.dims["day"] {
			if a.bucket == "1h" {
				r.date = stamp(u.Start)
			} else {
				r.date = day(u.Start)
			}
		}
		if a.dims["model"] {
			r.model = u.Model
		}
		if a.dims["key"] {
			r.key = n.key(u.APIKeyID)
			if u.NoKeySplit {
				r.key = "(not split by key)"
			}
		}
		if a.dims["project"] {
			r.project = n.project(t, u.ProjectID)
		}
		k := strings.Join([]string{r.date, r.provider, r.org, r.kind, r.project, r.key, r.model}, "\x00")
		cur, ok := a.rows[k]
		if !ok {
			cur = &r
			cur.priced = true
			a.rows[k] = cur
			a.order = append(a.order, k)
		}
		cur.requests += u.Requests
		cur.input += u.Input
		cur.cached += u.CachedInput
		cur.write += u.CacheWrite
		cur.output += u.Output
		if u.Unit == "bytes" {
			// Stored bytes are a level, not a flow: keep the peak.
			cur.quantity = max(cur.quantity, u.Quantity)
		} else {
			cur.quantity += u.Quantity
		}
		if u.Unit != "" {
			a.hasQty = true
			cur.priced = false
			continue
		}
		if est, ok := estimate(a.prices, t.org.Provider, u); ok {
			cur.est += est
		} else if u.Input+u.Output > 0 {
			cur.priced = false
			a.unpriced[t.org.Provider+"/"+u.Model] = true
		}
	}
}

func (a *usageAgg) emit() error {
	list := make([]*usageRow, 0, len(a.order))
	for _, k := range a.order {
		list = append(list, a.rows[k])
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].date != list[j].date {
			return list[i].date < list[j].date
		}
		return list[i].est > list[j].est
	})
	var cols []string
	if a.dims["day"] {
		cols = append(cols, "date")
	}
	cols = append(cols, "provider", "org", "kind")
	if a.dims["project"] {
		cols = append(cols, "project")
	}
	if a.dims["key"] {
		cols = append(cols, "key")
	}
	if a.dims["model"] {
		cols = append(cols, "model")
	}
	if a.hasOA {
		cols = append(cols, "requests")
	}
	cols = append(cols, "input", "cached", "cache_write", "output")
	if a.hasQty {
		cols = append(cols, "quantity", "unit")
	}
	cols = append(cols, "est_usd")

	rows := make([]map[string]any, 0, len(list))
	var totIn, totOut int64
	var totEst float64
	for _, r := range list {
		row := map[string]any{
			"date": r.date, "provider": r.provider, "org": r.org, "kind": r.kind, "project": r.project,
			"key": r.key, "model": r.model, "input": r.input, "cached": r.cached,
			"cache_write": r.write, "output": r.output,
		}
		if r.provider == "openai" {
			row["requests"] = r.requests
		}
		if r.unit != "" {
			row["quantity"], row["unit"] = r.quantity, r.unit
		}
		if r.unit != "" {
			// not priced here; see 'llmspend cost'
		} else if r.priced {
			row["est_usd"] = usd(r.est)
		} else if r.est > 0 {
			row["est_usd"] = usd(r.est) // partial: some models unpriced
		}
		rows = append(rows, row)
		totIn += r.input
		totOut += r.output
		totEst += r.est
	}
	if err := emitRows(rows, cols); err != nil {
		return err
	}
	if len(rows) > 0 {
		footer("Total: %d input / %d output tokens, est %s for token kinds. Estimates use the local price table at standard rates; 'llmspend cost' has the billed amount for every kind.", totIn, totOut, money(totEst))
	}
	if len(a.unpriced) > 0 {
		ms := make([]string, 0, len(a.unpriced))
		for m := range a.unpriced {
			ms = append(ms, m)
		}
		sort.Strings(ms)
		fmt.Fprintf(stderr, "note: no price for %s; est_usd leaves them out (try 'llmspend prices refresh')\n", strings.Join(ms, ", "))
	}
	return nil
}

func init() {
	f := usageCmd.Flags()
	f.StringSliceVar(&usageKinds, "kind", nil, "only these kinds (comma-separated): completions, embeddings, moderations, decisions, images, speech, transcription, web_search, file_search, code_interpreter, vector_stores, messages")
	f.StringVar(&usageSince, "since", "7d", "start: 7d, 24h, 2w, YYYY-MM-DD or RFC 3339")
	f.StringVar(&usageUntil, "until", "", "end (exclusive; a bare date includes that day); default now")
	f.StringSliceVar(&usageBy, "by", []string{"model"}, "group by model, key, project, day (comma-separated)")
	f.StringVar(&usageBucket, "bucket", "1d", "bucket width for --by day: 1d or 1h")
	rootCmd.AddCommand(usageCmd)
}
