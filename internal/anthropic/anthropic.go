// Package anthropic reads the Anthropic Admin API (usage and cost reports,
// workspaces, API keys, Claude Code analytics) with an Admin API key.
// GET only.
package anthropic

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/iamnikolie/llmspend/internal/client"
	"github.com/iamnikolie/llmspend/internal/spend"
)

// Version is the anthropic-version header sent on every call.
const Version = "2023-06-01"

// API is one Anthropic org.
type API struct {
	C *client.Client
}

// New wraps a client already pointed at the org's base URL and key.
func New(c *client.Client) *API {
	c.Provider = "anthropic"
	c.AuthStyle = client.AuthXAPIKey
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	c.Headers["anthropic-version"] = Version
	return &API{C: c}
}

type report[T any] struct {
	Data []struct {
		StartingAt time.Time `json:"starting_at"`
		EndingAt   time.Time `json:"ending_at"`
		Results    []T       `json:"results"`
	} `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
}

type usageResult struct {
	APIKeyID      *string `json:"api_key_id"`
	WorkspaceID   *string `json:"workspace_id"`
	Model         *string `json:"model"`
	Uncached      int64   `json:"uncached_input_tokens"`
	CacheRead     int64   `json:"cache_read_input_tokens"`
	CacheCreation struct {
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
	Output        int64 `json:"output_tokens"`
	ServerToolUse struct {
		WebSearchRequests int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
}

type costResult struct {
	Amount      string  `json:"amount"` // cents, decimal string
	Currency    string  `json:"currency"`
	Description *string `json:"description"`
	Model       *string `json:"model"`
	CostType    *string `json:"cost_type"`
	WorkspaceID *string `json:"workspace_id"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func timeRange(from, to time.Time) url.Values {
	q := url.Values{}
	q.Set("starting_at", from.UTC().Format(time.RFC3339))
	if !to.IsZero() {
		q.Set("ending_at", to.UTC().Format(time.RFC3339))
	}
	return q
}

func maxBuckets(width string) string {
	switch width {
	case "1h":
		return "168"
	case "1m":
		return "1440"
	}
	return "31"
}

// Usage reads /organizations/usage_report/messages. groupBy uses Anthropic
// field names (model, api_key_id, workspace_id).
func (a *API) Usage(ctx context.Context, from, to time.Time, width string, groupBy []string) ([]spend.Usage, error) {
	if width == "" {
		width = "1d"
	}
	q := timeRange(from, to)
	q.Set("bucket_width", width)
	q.Set("limit", maxBuckets(width))
	for _, g := range groupBy {
		q.Add("group_by[]", g)
	}
	var out []spend.Usage
	for {
		var r report[usageResult]
		if _, err := a.C.GetJSON(ctx, "/organizations/usage_report/messages", q, &r); err != nil {
			return nil, err
		}
		for _, b := range r.Data {
			for _, x := range b.Results {
				write := x.CacheCreation.Ephemeral5m + x.CacheCreation.Ephemeral1h
				out = append(out, spend.Usage{
					Start:        b.StartingAt.UTC(),
					Model:        str(x.Model),
					APIKeyID:     str(x.APIKeyID),
					ProjectID:    str(x.WorkspaceID),
					Input:        x.Uncached + x.CacheRead + write,
					CachedInput:  x.CacheRead,
					CacheWrite:   write,
					CacheWrite1h: x.CacheCreation.Ephemeral1h,
					Output:       x.Output,
					WebSearches:  x.ServerToolUse.WebSearchRequests,
				})
			}
		}
		if !r.HasMore || r.NextPage == "" {
			return out, nil
		}
		q.Set("page", r.NextPage)
	}
}

// Costs reads /organizations/cost_report (daily). groupBy uses Anthropic
// field names (workspace_id, description). Amounts arrive in cents.
func (a *API) Costs(ctx context.Context, from, to time.Time, groupBy []string) ([]spend.Cost, error) {
	q := timeRange(from, to)
	q.Set("bucket_width", "1d")
	q.Set("limit", "31")
	for _, g := range groupBy {
		q.Add("group_by[]", g)
	}
	var out []spend.Cost
	for {
		var r report[costResult]
		if _, err := a.C.GetJSON(ctx, "/organizations/cost_report", q, &r); err != nil {
			return nil, err
		}
		for _, b := range r.Data {
			for _, x := range b.Results {
				cents, _ := strconv.ParseFloat(x.Amount, 64)
				model := str(x.Model)
				if model == "" && x.CostType != nil && *x.CostType != "tokens" {
					model = "(" + *x.CostType + ")"
				}
				out = append(out, spend.Cost{
					Start:     b.StartingAt.UTC(),
					ProjectID: str(x.WorkspaceID),
					LineItem:  str(x.Description),
					Model:     model,
					USD:       cents / 100,
				})
			}
		}
		if !r.HasMore || r.NextPage == "" {
			return out, nil
		}
		q.Set("page", r.NextPage)
	}
}

// Org is the organization behind the admin key.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Me reads /organizations/me.
func (a *API) Me(ctx context.Context) (*Org, error) {
	var o Org
	if _, err := a.C.GetJSON(ctx, "/organizations/me", nil, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

type list[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

func listAll[T any](ctx context.Context, a *API, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "1000")
	var out []T
	for {
		var p list[T]
		if _, err := a.C.GetJSON(ctx, path, q, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || p.LastID == "" {
			return out, nil
		}
		q.Set("after_id", p.LastID)
	}
}

// Workspaces lists workspaces, the default one included.
func (a *API) Workspaces(ctx context.Context, includeArchived bool) ([]spend.Project, error) {
	type ws struct {
		ID         string     `json:"id"`
		Name       string     `json:"name"`
		CreatedAt  time.Time  `json:"created_at"`
		ArchivedAt *time.Time `json:"archived_at"`
	}
	q := url.Values{}
	q.Set("include_default", "true")
	if includeArchived {
		q.Set("include_archived", "true")
	}
	xs, err := listAll[ws](ctx, a, "/organizations/workspaces", q)
	if err != nil {
		return nil, err
	}
	out := make([]spend.Project, 0, len(xs))
	for _, w := range xs {
		status := "active"
		if w.ArchivedAt != nil {
			status = "archived"
		}
		out = append(out, spend.Project{ID: w.ID, Name: w.Name, Status: status, Created: w.CreatedAt, Archived: w.ArchivedAt != nil})
	}
	return out, nil
}

// Keys lists API keys in every status. The Admin API does not list Admin
// keys themselves.
func (a *API) Keys(ctx context.Context) ([]spend.Key, error) {
	type key struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Hint      *string   `json:"partial_key_hint"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
		CreatedBy *struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"created_by"`
		Scope struct {
			Type        string `json:"type"`
			WorkspaceID string `json:"workspace_id"`
		} `json:"scope"`
		WorkspaceID string `json:"workspace_id"` // older responses
	}
	xs, err := listAll[key](ctx, a, "/organizations/api_keys", nil)
	if err != nil {
		return nil, err
	}
	out := make([]spend.Key, 0, len(xs))
	for _, k := range xs {
		ws := k.Scope.WorkspaceID
		if ws == "" {
			ws = k.WorkspaceID
		}
		kind := "workspace"
		if k.Scope.Type == "organization" {
			kind = "organization"
		}
		owner := ""
		if k.CreatedBy != nil {
			owner = k.CreatedBy.ID
		}
		out = append(out, spend.Key{
			ID: k.ID, Name: k.Name, Hint: str(k.Hint), Kind: kind, ProjectID: ws,
			Owner: owner, Status: k.Status, Created: k.CreatedAt,
		})
	}
	return out, nil
}

// ClaudeCodeRecord is one actor's Claude Code day.
type ClaudeCodeRecord struct {
	Date  time.Time `json:"date"`
	Actor struct {
		Type       string `json:"type"`
		Email      string `json:"email_address"`
		APIKeyName string `json:"api_key_name"`
	} `json:"actor"`
	CustomerType string `json:"customer_type"`
	TerminalType string `json:"terminal_type"`
	IsRemote     bool   `json:"is_remote"`
	CoreMetrics  struct {
		Commits     int64 `json:"commits_by_claude_code"`
		PRs         int64 `json:"pull_requests_by_claude_code"`
		NumSessions int64 `json:"num_sessions"`
		Lines       struct {
			Added   int64 `json:"added"`
			Removed int64 `json:"removed"`
		} `json:"lines_of_code"`
	} `json:"core_metrics"`
	Models []struct {
		Model  string `json:"model"`
		Tokens struct {
			Input         int64 `json:"input"`
			Output        int64 `json:"output"`
			CacheRead     int64 `json:"cache_read"`
			CacheCreation int64 `json:"cache_creation"`
		} `json:"tokens"`
		EstimatedCost struct {
			Amount   float64 `json:"amount"` // cents
			Currency string  `json:"currency"`
		} `json:"estimated_cost"`
	} `json:"model_breakdown"`
}

// Actor names who did the work: an email or an API key name.
func (r ClaudeCodeRecord) ActorName() string {
	if r.Actor.Email != "" {
		return r.Actor.Email
	}
	return r.Actor.APIKeyName
}

// ClaudeCode reads /organizations/usage_report/claude_code for one UTC day.
func (a *API) ClaudeCode(ctx context.Context, day time.Time) ([]ClaudeCodeRecord, error) {
	q := url.Values{}
	q.Set("starting_at", day.UTC().Format("2006-01-02"))
	q.Set("limit", "1000")
	var out []ClaudeCodeRecord
	for {
		var p struct {
			Data     []ClaudeCodeRecord `json:"data"`
			HasMore  bool               `json:"has_more"`
			NextPage string             `json:"next_page"`
		}
		if _, err := a.C.GetJSON(ctx, "/organizations/usage_report/claude_code", q, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || p.NextPage == "" {
			return out, nil
		}
		q.Set("page", p.NextPage)
	}
}
