package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/iamnikolie/llmspend/internal/spend"
)

// ErrNoBillingExport is returned by Costs when no billing export table is
// configured: Google has no other API for billed Gemini cost.
var ErrNoBillingExport = errors.New("gemini: no billed-cost API; Cloud Billing export to BigQuery is not configured — fix: enable it ('llmspend skill' → Gemini) and set billing_table; until then 'llmspend usage -p gemini' estimates the cost")

// exportLag is how many days after a usage day its export rows may still
// land in the table's daily partitions.
const exportLag = 5

// The billing export columns Costs reads.
var billingFields = []string{"service", "sku", "project", "usage_start_time", "cost", "currency", "currency_conversion_rate", "credits"}

type bqField struct {
	Name   string    `json:"name"`
	Type   string    `json:"type"`
	Mode   string    `json:"mode"`
	Fields []bqField `json:"fields"`
}

type bqCell struct {
	V json.RawMessage `json:"v"`
}

// decode turns a tabledata row into a map by the schema: RECORD → map,
// REPEATED → []any, leaves → the string BigQuery sends (or nil).
func decode(fields []bqField, cells []bqCell) map[string]any {
	out := map[string]any{}
	for i, f := range fields {
		if i >= len(cells) {
			break
		}
		out[f.Name] = decodeValue(f, cells[i].V)
	}
	return out
}

func decodeValue(f bqField, raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if f.Mode == "REPEATED" {
		var items []bqCell
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		one := f
		one.Mode = ""
		list := make([]any, 0, len(items))
		for _, it := range items {
			list = append(list, decodeValue(one, it.V))
		}
		return list
	}
	if f.Type == "RECORD" || f.Type == "STRUCT" {
		var rec struct {
			F []bqCell `json:"f"`
		}
		if json.Unmarshal(raw, &rec) != nil {
			return nil
		}
		return decode(f.Fields, rec.F)
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return s
}

func str(m map[string]any, path ...string) string {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = mm[p]
	}
	s, _ := v.(string)
	return s
}

func num(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// splitTable reads "project.dataset.table" (or "project:dataset.table").
func splitTable(s string) (project, dataset, table string, err error) {
	s = strings.Replace(s, ":", ".", 1)
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("gemini: billing_table %q: use project.dataset.table, e.g. my-billing.billing_export.gcp_billing_export_v1_XXXXXX_XXXXXX_XXXXXX", s)
	}
	return parts[0], parts[1], parts[2], nil
}

// isGemini keeps the billing rows of the Gemini API (the service was named
// "Generative Language API" before it was "Gemini API").
func isGemini(service string) bool {
	s := strings.ToLower(service)
	return strings.Contains(s, "gemini") || strings.Contains(s, "generative language")
}

// Costs reads billed cost from the Cloud Billing standard usage cost export
// in BigQuery, with GET calls only: the table's schema, then tabledata.list
// on each daily partition from the first day of the range to exportLag days
// after its end (rows land in the partition of the day they were exported).
// Rows are kept when the service is the Gemini API, the project is one of
// this org's, and usage_start_time is in [from, to). The amount is cost
// plus credits, converted to USD with the row's currency_conversion_rate;
// Amount and Currency keep it in the billing account's currency.
func (a *API) Costs(ctx context.Context, from, to time.Time) ([]spend.Cost, error) {
	if a.BillingTable == "" {
		return nil, ErrNoBillingExport
	}
	proj, ds, tbl, err := splitTable(a.BillingTable)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	if ids, err := a.activeIDs(ctx); err == nil {
		for _, id := range ids {
			keep[id] = true
		}
	}
	base := "/projects/" + url.PathEscape(proj) + "/datasets/" + url.PathEscape(ds) + "/tables/"
	var meta struct {
		Schema struct {
			Fields []bqField `json:"fields"`
		} `json:"schema"`
		TimePartitioning *struct {
			Type string `json:"type"`
		} `json:"timePartitioning"`
	}
	if err := a.get(ctx, a.BigQuery, base+url.PathEscape(tbl), nil, &meta); err != nil {
		return nil, fmt.Errorf("billing export table %s: %w", a.BillingTable, err)
	}
	want := map[string]bool{}
	for _, f := range billingFields {
		want[f] = true
	}
	var fields []bqField // selected columns come back in schema order
	for _, f := range meta.Schema.Fields {
		if want[f.Name] {
			fields = append(fields, f)
		}
	}
	if len(fields) < 5 {
		return nil, fmt.Errorf("gemini: %s does not look like a Cloud Billing usage cost export (no cost/usage_start_time columns)", a.BillingTable)
	}

	end := to
	if end.IsZero() {
		end = a.Now().UTC()
	}
	tables := []string{tbl} // not partitioned: read it whole
	if meta.TimePartitioning != nil && meta.TimePartitioning.Type == "DAY" {
		tables = nil
		last := end.AddDate(0, 0, exportLag)
		if today := a.Now().UTC(); last.After(today) {
			last = today
		}
		for d := from.UTC().Truncate(24 * time.Hour); !d.After(last); d = d.AddDate(0, 0, 1) {
			tables = append(tables, tbl+"$"+d.Format("20060102"))
		}
	}

	var out []spend.Cost
	for _, t := range tables {
		q := url.Values{"selectedFields": {strings.Join(billingFields, ",")}, "maxResults": {"10000"}}
		for {
			var page struct {
				Rows []struct {
					F []bqCell `json:"f"`
				} `json:"rows"`
				PageToken string `json:"pageToken"`
			}
			if err := a.get(ctx, a.BigQuery, base+url.PathEscape(t)+"/data", q, &page); err != nil {
				return nil, fmt.Errorf("billing export %s: %w", t, err)
			}
			for _, r := range page.Rows {
				row := decode(fields, r.F)
				if !isGemini(str(row, "service", "description")) {
					continue
				}
				project := str(row, "project", "id")
				if len(keep) > 0 && !keep[project] {
					continue
				}
				start := time.Unix(0, int64(num(str(row, "usage_start_time"))*1e9)).UTC()
				if start.Before(from) || !start.Before(end) {
					continue
				}
				amount := num(str(row, "cost"))
				if cs, ok := row["credits"].([]any); ok {
					for _, c := range cs {
						if m, ok := c.(map[string]any); ok {
							amount += num(str(m, "amount"))
						}
					}
				}
				usd := amount
				if rate := num(str(row, "currency_conversion_rate")); rate > 0 {
					usd = amount / rate
				}
				sku := str(row, "sku", "description")
				out = append(out, spend.Cost{
					Start: start.Truncate(24 * time.Hour), ProjectID: project,
					LineItem: sku, Model: sku, USD: usd,
					Amount: amount, Currency: str(row, "currency"),
				})
			}
			if page.PageToken == "" {
				break
			}
			q.Set("pageToken", page.PageToken)
		}
	}
	return out, nil
}
