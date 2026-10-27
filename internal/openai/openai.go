// Package openai reads the OpenAI Administration API (usage, costs,
// projects, keys, spend limits) with an admin key. GET only.
package openai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/iamnikolie/llmspend/internal/client"
	"github.com/iamnikolie/llmspend/internal/spend"
)

// API is one OpenAI org.
type API struct {
	C *client.Client
	// Org is the organization id from the openai-organization response
	// header of the last call, when OpenAI sends it.
	Org string
}

// New wraps a client already pointed at the org's base URL and key.
func New(c *client.Client) *API {
	c.Provider = "openai"
	c.AuthStyle = client.AuthBearer
	return &API{C: c}
}

func (a *API) get(ctx context.Context, path string, q url.Values, v any) error {
	h, err := a.C.GetJSON(ctx, path, q, v)
	if err != nil {
		return err
	}
	if o := h.Get("openai-organization"); o != "" {
		a.Org = o
	}
	return nil
}

type page[T any] struct {
	Data     []T    `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
	LastID   string `json:"last_id"`
}

type bucket[T any] struct {
	StartTime int64 `json:"start_time"`
	EndTime   int64 `json:"end_time"`
	Results   []T   `json:"results"`
}

// usageResult has every field any usage family returns; each family fills
// its own subset.
type usageResult struct {
	InputTokens       int64   `json:"input_tokens"`
	InputCachedTokens int64   `json:"input_cached_tokens"`
	InputCacheWrite   int64   `json:"input_cache_write_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	NumModelRequests  int64   `json:"num_model_requests"`
	Images            int64   `json:"images"`
	Characters        int64   `json:"characters"`
	Seconds           int64   `json:"seconds"`
	NumSessions       int64   `json:"num_sessions"`
	NumRequests       int64   `json:"num_requests"`
	UsageBytes        int64   `json:"usage_bytes"`
	ProjectID         *string `json:"project_id"`
	APIKeyID          *string `json:"api_key_id"`
	Model             *string `json:"model"`
}

// Kind is one usage family: /organization/usage/<Endpoint>.
type Kind struct {
	Name     string   // short name shown in the kind column and taken by --kind
	Endpoint string   // path segment under /organization/usage/
	GroupBy  []string // group_by fields the endpoint accepts (of model, api_key_id, project_id)
	Unit     string   // "" for token kinds
}

// Kinds lists every usage family the Usage API documents.
var Kinds = []Kind{
	{"completions", "completions", []string{"model", "api_key_id", "project_id"}, ""},
	{"embeddings", "embeddings", []string{"model", "api_key_id", "project_id"}, ""},
	{"moderations", "moderations", []string{"model", "api_key_id", "project_id"}, ""},
	{"decisions", "decisions", []string{"model", "api_key_id", "project_id"}, ""},
	{"images", "images", []string{"model", "api_key_id", "project_id"}, "images"},
	{"speech", "audio_speeches", []string{"model", "api_key_id", "project_id"}, "characters"},
	{"transcription", "audio_transcriptions", []string{"model", "api_key_id", "project_id"}, "seconds"},
	{"web_search", "web_search_calls", []string{"model", "api_key_id", "project_id"}, "calls"},
	{"file_search", "file_search_calls", []string{"api_key_id", "project_id"}, "calls"},
	{"code_interpreter", "code_interpreter_sessions", []string{"project_id"}, "sessions"},
	{"vector_stores", "vector_stores", []string{"project_id"}, "bytes"},
}

func (k Kind) quantity(r usageResult) int64 {
	switch k.Unit {
	case "images":
		return r.Images
	case "characters":
		return r.Characters
	case "seconds":
		return r.Seconds
	case "calls":
		return r.NumRequests
	case "sessions":
		return r.NumSessions
	case "bytes":
		return r.UsageBytes
	}
	return 0
}

type costsResult struct {
	Amount struct {
		Value    float64 `json:"value"`
		Currency string  `json:"currency"`
	} `json:"amount"`
	LineItem  *string `json:"line_item"`
	ProjectID *string `json:"project_id"`
	APIKeyID  *string `json:"api_key_id"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func timeRange(from, to time.Time) url.Values {
	q := url.Values{}
	q.Set("start_time", strconv.FormatInt(from.Unix(), 10))
	if !to.IsZero() {
		q.Set("end_time", strconv.FormatInt(to.Unix(), 10))
	}
	return q
}

// maxBuckets is the per-page bucket limit OpenAI allows for a bucket width.
func maxBuckets(width string) string {
	switch width {
	case "1h":
		return "168"
	case "1m":
		return "1440"
	}
	return "31"
}

// Usage reads one usage family. groupBy uses OpenAI field names (model,
// api_key_id, project_id); fields the family does not accept are dropped.
func (a *API) Usage(ctx context.Context, k Kind, from, to time.Time, width string, groupBy []string) ([]spend.Usage, error) {
	if width == "" {
		width = "1d"
	}
	q := timeRange(from, to)
	q.Set("bucket_width", width)
	q.Set("limit", maxBuckets(width))
	noKey := false
	for _, g := range groupBy {
		if slices.Contains(k.GroupBy, g) {
			q.Add("group_by", g)
		} else if g == "api_key_id" {
			noKey = true
		}
	}
	var out []spend.Usage
	for {
		var p page[bucket[usageResult]]
		if err := a.get(ctx, "/organization/usage/"+k.Endpoint, q, &p); err != nil {
			return nil, err
		}
		for _, b := range p.Data {
			for _, r := range b.Results {
				out = append(out, spend.Usage{
					Kind:        k.Name,
					Start:       time.Unix(b.StartTime, 0).UTC(),
					Model:       str(r.Model),
					APIKeyID:    str(r.APIKeyID),
					ProjectID:   str(r.ProjectID),
					Requests:    r.NumModelRequests,
					Input:       r.InputTokens,
					CachedInput: r.InputCachedTokens,
					CacheWrite:  r.InputCacheWrite,
					Output:      r.OutputTokens,
					Quantity:    k.quantity(r),
					Unit:        k.Unit,
					NoKeySplit:  noKey,
				})
			}
		}
		if !p.HasMore || p.NextPage == "" {
			return out, nil
		}
		q.Set("page", p.NextPage)
	}
}

// Costs reads /organization/costs (daily buckets). groupBy uses OpenAI field
// names (project_id, line_item, api_key_id). When grouped by line_item, the
// model is the part of the line item before ", " ("gpt-5, input").
func (a *API) Costs(ctx context.Context, from, to time.Time, groupBy []string) ([]spend.Cost, error) {
	q := timeRange(from, to)
	q.Set("bucket_width", "1d")
	q.Set("limit", "180")
	for _, g := range groupBy {
		q.Add("group_by", g)
	}
	var out []spend.Cost
	for {
		var p page[bucket[costsResult]]
		if err := a.get(ctx, "/organization/costs", q, &p); err != nil {
			return nil, err
		}
		for _, b := range p.Data {
			for _, r := range b.Results {
				li := str(r.LineItem)
				out = append(out, spend.Cost{
					Start:     time.Unix(b.StartTime, 0).UTC(),
					ProjectID: str(r.ProjectID),
					APIKeyID:  str(r.APIKeyID),
					LineItem:  li,
					Model:     ModelOfLineItem(li),
					USD:       r.Amount.Value,
				})
			}
		}
		if !p.HasMore || p.NextPage == "" {
			return out, nil
		}
		q.Set("page", p.NextPage)
	}
}

// ModelOfLineItem extracts the model from a cost line item such as
// "gpt-5-mini-2025-08-07, input" or "gpt-4o, cached input".
func ModelOfLineItem(li string) string {
	if i := strings.LastIndex(li, ", "); i > 0 {
		return li[:i]
	}
	return li
}

type project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	ArchivedAt *int64 `json:"archived_at"`
}

// list walks an id-cursor list endpoint (after=last_id).
func list[T any](ctx context.Context, a *API, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "100")
	var out []T
	for {
		var p page[T]
		if err := a.get(ctx, path, q, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || p.LastID == "" {
			return out, nil
		}
		q.Set("after", p.LastID)
	}
}

func unix(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0).UTC()
}

// Projects lists the org's projects.
func (a *API) Projects(ctx context.Context, includeArchived bool) ([]spend.Project, error) {
	q := url.Values{}
	if includeArchived {
		q.Set("include_archived", "true")
	}
	ps, err := list[project](ctx, a, "/organization/projects", q)
	if err != nil {
		return nil, err
	}
	out := make([]spend.Project, 0, len(ps))
	for _, p := range ps {
		out = append(out, spend.Project{ID: p.ID, Name: p.Name, Status: p.Status, Created: unix(p.CreatedAt), Archived: p.ArchivedAt != nil})
	}
	return out, nil
}

type owner struct {
	Type string `json:"type"`
	Name string `json:"name"`
	User *struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
	ServiceAccount *struct {
		Name string `json:"name"`
	} `json:"service_account"`
}

func (o owner) String() string {
	switch {
	case o.User != nil && o.User.Name != "":
		return o.User.Name
	case o.User != nil:
		return o.User.Email
	case o.ServiceAccount != nil:
		return o.ServiceAccount.Name
	case o.Name != "":
		return o.Name
	}
	return o.Type
}

type apiKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RedactedValue string `json:"redacted_value"`
	CreatedAt     int64  `json:"created_at"`
	LastUsedAt    *int64 `json:"last_used_at"`
	OwnerAccess   string `json:"owner_project_access"`
	Owner         owner  `json:"owner"`
}

func lastUsed(p *int64) time.Time {
	if p == nil {
		return time.Time{}
	}
	return unix(*p)
}

// ProjectKeys lists one project's API keys.
func (a *API) ProjectKeys(ctx context.Context, p spend.Project) ([]spend.Key, error) {
	ks, err := list[apiKey](ctx, a, "/organization/projects/"+url.PathEscape(p.ID)+"/api_keys", nil)
	if err != nil {
		return nil, err
	}
	out := make([]spend.Key, 0, len(ks))
	for _, k := range ks {
		kind := "project"
		if k.Owner.Type == "service_account" {
			kind = "service_account"
		}
		out = append(out, spend.Key{
			ID: k.ID, Name: k.Name, Hint: k.RedactedValue, Kind: kind,
			ProjectID: p.ID, Project: p.Name, Owner: k.Owner.String(), Status: k.OwnerAccess,
			Created: unix(k.CreatedAt), LastUsed: lastUsed(k.LastUsedAt),
		})
	}
	return out, nil
}

// AdminKeys lists the org's admin keys.
func (a *API) AdminKeys(ctx context.Context) ([]spend.Key, error) {
	ks, err := list[apiKey](ctx, a, "/organization/admin_api_keys", nil)
	if err != nil {
		return nil, err
	}
	out := make([]spend.Key, 0, len(ks))
	for _, k := range ks {
		out = append(out, spend.Key{
			ID: k.ID, Name: k.Name, Hint: k.RedactedValue, Kind: "admin",
			Owner: k.Owner.String(), Created: unix(k.CreatedAt), LastUsed: lastUsed(k.LastUsedAt),
		})
	}
	return out, nil
}

// SpendLimit is a hard monthly limit.
type SpendLimit struct {
	USD       float64
	Interval  string
	Enforcing string
}

type spendLimit struct {
	ThresholdAmount int64  `json:"threshold_amount"` // cents
	Interval        string `json:"interval"`
	Enforcement     struct {
		Status string `json:"status"`
	} `json:"enforcement"`
}

func (a *API) spendLimit(ctx context.Context, path string) (*SpendLimit, error) {
	var s spendLimit
	err := a.get(ctx, path, nil, &s)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &SpendLimit{USD: float64(s.ThresholdAmount) / 100, Interval: s.Interval, Enforcing: s.Enforcement.Status}, nil
}

// SpendLimit reads the org's hard spend limit; nil when none is set.
func (a *API) SpendLimit(ctx context.Context) (*SpendLimit, error) {
	return a.spendLimit(ctx, "/organization/spend_limit")
}

// ProjectSpendLimit reads a project's hard spend limit; nil when none is set.
func (a *API) ProjectSpendLimit(ctx context.Context, projectID string) (*SpendLimit, error) {
	return a.spendLimit(ctx, "/organization/projects/"+url.PathEscape(projectID)+"/spend_limit")
}

// SpendAlert is an email alert at a monthly threshold.
type SpendAlert struct {
	ID       string
	USD      float64
	Interval string
}

// SpendAlerts lists the org's spend alerts.
func (a *API) SpendAlerts(ctx context.Context) ([]SpendAlert, error) {
	type alert struct {
		ID              string `json:"id"`
		ThresholdAmount int64  `json:"threshold_amount"`
		Interval        string `json:"interval"`
	}
	as, err := list[alert](ctx, a, "/organization/spend_alerts", nil)
	if err != nil {
		return nil, err
	}
	out := make([]SpendAlert, 0, len(as))
	for _, x := range as {
		out = append(out, SpendAlert{ID: x.ID, USD: float64(x.ThresholdAmount) / 100, Interval: x.Interval})
	}
	return out, nil
}
