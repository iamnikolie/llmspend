package cmd

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/iamnikolie/llmspend/internal/gemini"
	"github.com/iamnikolie/llmspend/internal/openai"
	"github.com/iamnikolie/llmspend/internal/spend"
	"github.com/spf13/cobra"
)

var (
	budgetProjects bool
	budgetAlertAt  float64
)

// Where the API stops: neither provider exposes the prepaid credit balance.
const (
	openaiBillingURL    = "https://platform.openai.com/settings/organization/billing/overview"
	anthropicBillingURL = "https://platform.claude.com/settings/billing"
)

var budgetCmd = &cobra.Command{
	Use:     "budget",
	Aliases: []string{"balance", "limits"},
	Short:   "Spend limit and alerts against month-to-date cost, per org (and project)",
	Long: "Month-to-date billed cost next to the hard spend limit and spend alerts the API\n" +
		"exposes. OpenAI: org and project spend limits plus org spend alerts. Anthropic:\n" +
		"Console orgs have no spend-limit API (it is Enterprise-only), so only the cost shows.\n" +
		"Neither API returns the prepaid credit balance; the note column links the billing page.\n" +
		"Gemini: month-to-date cost from the billing export when billing_table is set; no\n" +
		"spend-limit or prepay-balance API either way.\n\n" +
		"--alert-at 80 exits with status 3 when any limit is at or above 80% used, for cron.",
	Example: `  llmspend budget
  llmspend budget -p openai --projects
  llmspend budget --alert-at 80 || notify "LLM budget"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, _, _, err := monthRange("current")
		if err != nil {
			return err
		}
		ts, err := targets("")
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()
		var rows []map[string]any
		var over []string
		check := func(row map[string]any, name string) {
			if p, ok := row["used_pct"].(float64); ok && budgetAlertAt > 0 && p >= budgetAlertAt {
				over = append(over, fmt.Sprintf("%s %.0f%%", name, p))
			}
		}
		err = eachOrg(ts, func(t target) error {
			if t.gm != nil {
				row := map[string]any{"provider": t.org.Provider, "org": t.org.Name, "scope": "org"}
				cs, err := t.gm.Costs(ctx, from, time.Time{})
				switch {
				case errors.Is(err, gemini.ErrNoBillingExport):
					row["note"] = "no cost, limit or prepay-balance API; estimate with 'llmspend usage -p gemini --since " + day(from) + "', balance in Google AI Studio → Billing"
				case err != nil:
					return err
				default:
					row["spent_mtd"] = usd(sumCost(cs))
					row["note"] = "billing export; no limit or prepay-balance API, see Google AI Studio → Billing"
				}
				rows = append(rows, row)
				return nil
			}
			if t.an != nil {
				cs, err := t.an.Costs(ctx, from, time.Time{}, nil)
				if err != nil {
					return err
				}
				rows = append(rows, map[string]any{
					"provider": t.org.Provider, "org": t.org.Name, "scope": "org",
					"spent_mtd": usd(sumCost(cs)),
					"note":      "no spend-limit or balance API for Console orgs; see " + anthropicBillingURL,
				})
				return nil
			}
			group := []string(nil)
			if budgetProjects {
				group = []string{"project_id"}
			}
			cs, err := t.oa.Costs(ctx, from, time.Time{}, group)
			if err != nil {
				return err
			}
			lim, err := t.oa.SpendLimit(ctx)
			if err != nil {
				return err
			}
			alerts, err := t.oa.SpendAlerts(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "! %s: spend alerts not read: %v\n", t.org.Name, err)
			}
			var at []string
			for _, a := range alerts {
				at = append(at, money(a.USD))
			}
			row := limitRow(t, "org", sumCost(cs), lim)
			if len(at) > 0 {
				row["alerts"] = strings.Join(at, ", ")
			}
			row["note"] = "credit balance is not in the API; see " + openaiBillingURL
			check(row, t.org.Name)
			rows = append(rows, row)
			if !budgetProjects {
				return nil
			}
			ps, err := t.oa.Projects(ctx, false)
			if err != nil {
				return err
			}
			spent := map[string]float64{}
			for _, c := range cs {
				spent[c.ProjectID] += c.USD
			}
			for _, p := range ps {
				pl, err := t.oa.ProjectSpendLimit(ctx, p.ID)
				if err != nil {
					fmt.Fprintf(stderr, "! %s: spend limit of %s not read: %v\n", t.org.Name, p.Name, err)
					continue
				}
				if pl == nil && spent[p.ID] == 0 {
					continue
				}
				pr := limitRow(t, "project "+p.Name, spent[p.ID], pl)
				check(pr, t.org.Name+"/"+p.Name)
				rows = append(rows, pr)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := emitRows(rows, []string{"provider", "org", "scope", "spent_mtd", "limit", "left", "used_pct", "enforcement", "alerts", "note"}); err != nil {
			return err
		}
		if len(over) > 0 {
			fmt.Fprintf(stderr, "over %.0f%%: %s\n", budgetAlertAt, strings.Join(over, "; "))
			return exitCode(3)
		}
		return nil
	},
}

func sumCost(cs []spend.Cost) float64 {
	var s float64
	for _, c := range cs {
		s += c.USD
	}
	return s
}

func limitRow(t target, scope string, spent float64, l *openai.SpendLimit) map[string]any {
	row := map[string]any{"provider": t.org.Provider, "org": t.org.Name, "scope": scope, "spent_mtd": usd(spent)}
	if l == nil {
		row["limit"] = "none"
		return row
	}
	row["limit"] = usd(l.USD)
	row["left"] = usd(l.USD - spent)
	if l.USD > 0 {
		row["used_pct"] = math.Round(spent/l.USD*1000) / 10
	}
	row["enforcement"] = l.Enforcing
	return row
}

func init() {
	budgetCmd.Flags().BoolVar(&budgetProjects, "projects", false, "OpenAI: also each project's spend limit and month-to-date cost")
	budgetCmd.Flags().Float64Var(&budgetAlertAt, "alert-at", 0, "exit 3 when any limit is at or above this percent used")
	rootCmd.AddCommand(budgetCmd)
}
