package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	ccSince string
	ccUntil string
	ccBy    []string
)

var ccDims = map[string]bool{"user": true, "model": true, "day": true, "terminal": true}

var claudeCodeCmd = &cobra.Command{
	Use:     "claude-code",
	Aliases: []string{"cc"},
	Short:   "Claude Code usage per user, model or day (Anthropic Claude Code Analytics API)",
	Long: "Daily Claude Code records from Anthropic's Claude Code Analytics API: sessions,\n" +
		"commits, PRs, lines, tokens and Anthropic's estimated cost. It covers Claude Code\n" +
		"used through the Console org (API keys, Team/Enterprise seats). Personal Pro/Max\n" +
		"subscriptions are not in it. Data lags about an hour; one call per day in range.",
	Example: `  llmspend claude-code                      # last 7 days per user
  llmspend claude-code --since 30d --by model
  llmspend cc --by day,user`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		for i, b := range ccBy {
			switch strings.ToLower(b) {
			case "actor", "users", "email", "key":
				ccBy[i] = "user"
			case "models":
				ccBy[i] = "model"
			case "date", "days":
				ccBy[i] = "day"
			}
		}
		dims, err := checkDims(ccBy, ccDims)
		if err != nil {
			return err
		}
		from, err := parseSince(ccSince)
		if err != nil {
			return err
		}
		to, err := parseUntil(ccUntil)
		if err != nil {
			return err
		}
		if to.IsZero() || to.After(now()) {
			to = now()
		}
		ts, err := targets("anthropic")
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()

		type agg struct {
			row                                    map[string]any
			sessions, commits, prs, added, removed int64
			input, output, cacheRead, cacheWrite   int64
			cents                                  float64
		}
		rows := map[string]*agg{}
		var order []string
		err = eachOrg(ts, func(t target) error {
			for d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC); d.Before(to); d = d.AddDate(0, 0, 1) {
				recs, err := t.an.ClaudeCode(ctx, d)
				if err != nil {
					return fmt.Errorf("%s: %w", day(d), err)
				}
				for _, r := range recs {
					base := map[string]any{"org": t.org.Name}
					if dims["day"] {
						base["date"] = day(r.Date)
					}
					if dims["user"] {
						base["user"] = r.ActorName()
					}
					if dims["terminal"] {
						base["terminal"] = r.TerminalType
					}
					keyOf := func(model string) string {
						return fmt.Sprint(base["org"], "\x00", base["date"], "\x00", base["user"], "\x00", base["terminal"], "\x00", model)
					}
					get := func(model string) *agg {
						if !dims["model"] {
							model = ""
						}
						k := keyOf(model)
						a, ok := rows[k]
						if !ok {
							row := map[string]any{}
							for kk, v := range base {
								row[kk] = v
							}
							if dims["model"] {
								row["model"] = model
							}
							a = &agg{row: row}
							rows[k] = a
							order = append(order, k)
						}
						return a
					}
					if !dims["model"] {
						a := get("")
						a.sessions += r.CoreMetrics.NumSessions
						a.commits += r.CoreMetrics.Commits
						a.prs += r.CoreMetrics.PRs
						a.added += r.CoreMetrics.Lines.Added
						a.removed += r.CoreMetrics.Lines.Removed
					}
					for _, m := range r.Models {
						a := get(m.Model)
						a.input += m.Tokens.Input
						a.output += m.Tokens.Output
						a.cacheRead += m.Tokens.CacheRead
						a.cacheWrite += m.Tokens.CacheCreation
						a.cents += m.EstimatedCost.Amount
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(order))
		var total float64
		for _, k := range order {
			a := rows[k]
			r := a.row
			if !dims["model"] {
				r["sessions"], r["commits"], r["prs"] = a.sessions, a.commits, a.prs
				r["lines_added"], r["lines_removed"] = a.added, a.removed
			}
			r["input"], r["output"], r["cache_read"], r["cache_write"] = a.input, a.output, a.cacheRead, a.cacheWrite
			r["est_usd"] = usd(a.cents / 100)
			total += a.cents / 100
			out = append(out, r)
		}
		sort.SliceStable(out, func(i, j int) bool {
			di, dj := fmt.Sprint(out[i]["date"]), fmt.Sprint(out[j]["date"])
			if di != dj {
				return di < dj
			}
			return out[i]["est_usd"].(float64) > out[j]["est_usd"].(float64)
		})
		var cols []string
		if dims["day"] {
			cols = append(cols, "date")
		}
		cols = append(cols, "org")
		for _, d := range []string{"user", "terminal", "model"} {
			if dims[d] {
				cols = append(cols, d)
			}
		}
		if !dims["model"] {
			cols = append(cols, "sessions", "commits", "prs", "lines_added", "lines_removed")
		}
		cols = append(cols, "input", "output", "cache_read", "cache_write", "est_usd")
		if err := emitRows(out, cols); err != nil {
			return err
		}
		if len(out) == 0 {
			fmt.Fprintln(stderr, "note: no Claude Code records; personal Pro/Max subscriptions are not reported by this API")
		}
		footer("Total est %s (Anthropic's estimate, since %s)", money(total), day(from))
		return nil
	},
}

func init() {
	f := claudeCodeCmd.Flags()
	f.StringVar(&ccSince, "since", "7d", "start day: 7d, 2w, YYYY-MM-DD")
	f.StringVar(&ccUntil, "until", "", "end (exclusive; a bare date includes that day); default now")
	f.StringSliceVar(&ccBy, "by", []string{"user"}, "group by user, model, day, terminal (comma-separated)")
	rootCmd.AddCommand(claudeCodeCmd)
}
