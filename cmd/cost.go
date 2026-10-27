package cmd

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/iamnikolie/llmspend/internal/spend"
	"github.com/spf13/cobra"
)

var (
	costMonth string
	costSince string
	costUntil string
	costBy    []string
)

var costDims = map[string]bool{"day": true, "project": true, "model": true, "line": true, "key": true}

var costCmd = &cobra.Command{
	Use:   "cost [month]",
	Short: "Billed cost from the providers' cost reports, month to date by default",
	Long: "Billed cost from OpenAI's Costs API and Anthropic's cost report, in USD.\n" +
		"Without --since, the range is one calendar month (UTC): the current one, or --month.\n" +
		"Anthropic's cost report has no per-key split (use 'usage --by key') and leaves out\n" +
		"Priority Tier; OpenAI's has no per-model field, so --by model reads it from line items.",
	Example: `  llmspend cost                        # this month so far, one row per org
  llmspend cost --month last --by model
  llmspend cost --month 2026-09 --by project
  llmspend cost --since 7d --by day`,
	// "--month" works bare (this month), so "--month last" reaches us as the
	// flag's default plus a positional "last"; a bare "cost 2026-09" works too.
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			costMonth = args[0]
			_ = cmd.Flags().Set("month", args[0])
		}
		dims, err := checkDims(costBy, costDims)
		if err != nil {
			return err
		}
		if dims["model"] && dims["line"] {
			delete(dims, "model")
		}
		var from, to time.Time
		label := ""
		if costSince != "" {
			if cmd.Flags().Changed("month") {
				return fmt.Errorf("use --month or --since, not both")
			}
			if from, err = parseSince(costSince); err != nil {
				return err
			}
			if to, err = parseUntil(costUntil); err != nil {
				return err
			}
			label = "since " + day(from)
		} else {
			if from, to, label, err = monthRange(costMonth); err != nil {
				return err
			}
		}
		ts, err := targets("")
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()

		agg := map[string]map[string]any{}
		var order []string
		var total float64
		err = eachOrg(ts, func(t target) error {
			cs, err := fetchCosts(ctx, t, from, to, dims)
			if err != nil {
				return err
			}
			n := resolveNames(ctx, t, dims["project"], dims["key"])
			for _, c := range cs {
				row := map[string]any{"provider": t.org.Provider, "org": t.org.Name}
				if dims["day"] {
					row["date"] = day(c.Start)
				}
				if dims["project"] {
					row["project"] = n.project(t, c.ProjectID)
				}
				if dims["key"] {
					row["key"] = n.key(c.APIKeyID)
				}
				if dims["model"] {
					row["model"] = c.Model
				}
				if dims["line"] {
					row["line_item"] = c.LineItem
				}
				k := fmt.Sprint(row["date"], "\x00", row["provider"], "\x00", row["org"], "\x00", row["project"], "\x00", row["key"], "\x00", row["model"], "\x00", row["line_item"])
				cur, ok := agg[k]
				if !ok {
					row["usd"] = 0.0
					agg[k], cur = row, row
					order = append(order, k)
				}
				cur["usd"] = cur["usd"].(float64) + c.USD
				total += c.USD
			}
			return nil
		})
		if err != nil {
			return err
		}
		rows := make([]map[string]any, 0, len(order))
		for _, k := range order {
			r := agg[k]
			r["usd"] = usd(r["usd"].(float64))
			rows = append(rows, r)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			di, dj := fmt.Sprint(rows[i]["date"]), fmt.Sprint(rows[j]["date"])
			if di != dj {
				return di < dj
			}
			return rows[i]["usd"].(float64) > rows[j]["usd"].(float64)
		})
		var cols []string
		if dims["day"] {
			cols = append(cols, "date")
		}
		cols = append(cols, "provider", "org")
		for _, d := range []string{"project", "key", "model"} {
			if dims[d] {
				cols = append(cols, d)
			}
		}
		if dims["line"] {
			cols = append(cols, "line_item")
		}
		cols = append(cols, "usd")
		if err := emitRows(rows, cols); err != nil {
			return err
		}
		footer("Total %s: %s", label, money(total))
		return nil
	},
}

func fetchCosts(ctx context.Context, t target, from, to time.Time, dims map[string]bool) ([]spend.Cost, error) {
	var group []string
	if t.oa != nil {
		if dims["project"] {
			group = append(group, "project_id")
		}
		if dims["key"] {
			group = append(group, "api_key_id")
		}
		if dims["model"] || dims["line"] {
			group = append(group, "line_item")
		}
		return t.oa.Costs(ctx, from, to, group)
	}
	if dims["key"] {
		return nil, fmt.Errorf("the Anthropic cost report cannot split by key; use 'llmspend usage --by key' for per-key tokens and estimates")
	}
	if dims["project"] {
		group = append(group, "workspace_id")
	}
	if dims["model"] || dims["line"] {
		group = append(group, "description")
	}
	return t.an.Costs(ctx, from, to, group)
}

func init() {
	f := costCmd.Flags()
	f.StringVar(&costMonth, "month", "current", "calendar month: current, last or YYYY-MM")
	f.Lookup("month").NoOptDefVal = "current"
	f.StringVar(&costSince, "since", "", "start instead of a month: 7d, 2w, YYYY-MM-DD or RFC 3339")
	f.StringVar(&costUntil, "until", "", "end with --since (exclusive; a bare date includes that day)")
	f.StringSliceVar(&costBy, "by", nil, "group by day, project, model, line, key (comma-separated)")
	rootCmd.AddCommand(costCmd)
}
