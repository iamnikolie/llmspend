// Package gemini reads Gemini API use from Google Cloud: token and request
// counts per project and model from Cloud Monitoring, project names from
// Cloud Resource Manager, and billed cost from the Cloud Billing export in
// BigQuery once one is configured. Google has no API for the Gemini prepay
// balance or for billed cost outside that export. GET only, like the rest
// of llmspend; the OAuth token comes from gcloud.
package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/iamnikolie/llmspend/internal/client"
	"github.com/iamnikolie/llmspend/internal/spend"
)

// API roots. A test or proxy base URL replaces the host and keeps these
// names as path prefixes.
const (
	MonitoringURL = "https://monitoring.googleapis.com/v3"
	CRMURL        = "https://cloudresourcemanager.googleapis.com/v1"
	BigQueryURL   = "https://bigquery.googleapis.com/bigquery/v2"
)

// Token returns an OAuth access token from gcloud: the named account's
// login, or Application Default Credentials when account is empty. It never
// changes gcloud's active account. Swapped in tests.
var Token = func(ctx context.Context, account string) (string, error) {
	args := []string{"auth", "application-default", "print-access-token"}
	fix := "run 'gcloud auth application-default login', or set account: in the profile to a gcloud login"
	if account != "" {
		args = []string{"auth", "print-access-token", "--account=" + account}
		fix = "run 'gcloud auth login " + account + "' (it adds the login without switching the active account)"
	}
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", errors.New("gemini: gcloud not found — fix: install the Google Cloud CLI (cloud.google.com/sdk) and log in")
	}
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		return "", fmt.Errorf("gemini: no gcloud token (%s) — fix: %s", msg, fix)
	}
	return strings.TrimSpace(string(out)), nil
}

// API is one Gemini "org": a set of Google Cloud projects, usually the
// gen-lang-client-* projects AI Studio creates, read with one gcloud login.
type API struct {
	Monitoring, CRM, BigQuery *client.Client

	Account      string   // gcloud account; "" = Application Default Credentials
	ProjectIDs   []string // configured projects; empty = discover gen-lang-client-*
	BillingTable string   // project.dataset.table of the Cloud Billing export
	Now          func() time.Time

	mu       sync.Mutex
	tokenErr error
	authed   bool
	projects []spend.Project
}

// New builds the three clients from a template carrying the HTTP client and
// verbosity. c.BaseURL, when set, replaces the googleapis.com hosts.
func New(c *client.Client, account string, projects []string, billingTable string) *API {
	mk := func(root, prefix string) *client.Client {
		cc := *c
		cc.Provider = "gemini"
		cc.AuthStyle = client.AuthBearer
		cc.Headers = nil
		cc.BaseURL = root
		if c.BaseURL != "" {
			cc.BaseURL = strings.TrimRight(c.BaseURL, "/") + prefix
		}
		return &cc
	}
	return &API{
		Monitoring:   mk(MonitoringURL, "/monitoring/v3"),
		CRM:          mk(CRMURL, "/crm/v1"),
		BigQuery:     mk(BigQueryURL, "/bigquery/v2"),
		Account:      account,
		ProjectIDs:   projects,
		BillingTable: billingTable,
		Now:          func() time.Time { return time.Now().UTC() },
	}
}

// Login names the credentials for 'orgs' and 'config show'.
func (a *API) Login() string {
	if a.Account == "" {
		return "gcloud ADC"
	}
	return "gcloud " + a.Account
}

func (a *API) auth(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authed || a.tokenErr != nil {
		return a.tokenErr
	}
	tok, err := Token(ctx, a.Account)
	if err != nil {
		a.tokenErr = err
		return err
	}
	for _, c := range []*client.Client{a.Monitoring, a.CRM, a.BigQuery} {
		c.Key = tok
	}
	a.authed = true
	return nil
}

// get is GetJSON with a token and Google's refusals turned into a reason
// and a fix.
func (a *API) get(ctx context.Context, c *client.Client, path string, q url.Values, v any) error {
	if err := a.auth(ctx); err != nil {
		return err
	}
	_, err := c.GetJSON(ctx, path, q, v)
	var e *client.APIError
	if errors.As(err, &e) {
		switch e.Status {
		case http.StatusUnauthorized:
			e.Reason = "the gcloud token was rejected"
			e.Fix = "log in again: 'gcloud auth login " + a.Account + "' (or 'gcloud auth application-default login' without account:)"
			if a.Account == "" {
				e.Fix = "log in again: 'gcloud auth application-default login'"
			}
		case http.StatusForbidden:
			e.Reason = "this login may not read it"
			e.Fix = "use the login that owns the AI Studio projects (account: in the profile); it needs Monitoring Viewer on each project, and BigQuery Data Viewer on the billing export dataset"
		}
	}
	return err
}

// Projects lists the configured projects with their names, or every
// gen-lang-client-* project the login can see (the projects AI Studio
// creates for API keys).
func (a *API) Projects(ctx context.Context) ([]spend.Project, error) {
	a.mu.Lock()
	cached := a.projects
	a.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	type project struct {
		ProjectID      string `json:"projectId"`
		Name           string `json:"name"`
		LifecycleState string `json:"lifecycleState"`
		CreateTime     string `json:"createTime"`
	}
	conv := func(p project) spend.Project {
		created, _ := time.Parse(time.RFC3339, p.CreateTime)
		return spend.Project{ID: p.ProjectID, Name: p.Name, Status: strings.ToLower(p.LifecycleState), Created: created,
			Archived: p.LifecycleState != "" && p.LifecycleState != "ACTIVE"}
	}
	var out []spend.Project
	if len(a.ProjectIDs) > 0 {
		for _, id := range a.ProjectIDs {
			var p project
			err := a.get(ctx, a.CRM, "/projects/"+url.PathEscape(id), nil, &p)
			var e *client.APIError
			if errors.As(err, &e) && e.Status != http.StatusUnauthorized {
				// No resourcemanager read on it: keep the id, usage may still work.
				out = append(out, spend.Project{ID: id, Name: id})
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, conv(p))
		}
	} else {
		q := url.Values{"filter": {"id:gen-lang-client-*"}}
		for {
			var page struct {
				Projects      []project `json:"projects"`
				NextPageToken string    `json:"nextPageToken"`
			}
			if err := a.get(ctx, a.CRM, "/projects", q, &page); err != nil {
				return nil, err
			}
			for _, p := range page.Projects {
				out = append(out, conv(p))
			}
			if page.NextPageToken == "" {
				break
			}
			q.Set("pageToken", page.NextPageToken)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("gemini: %s sees no gen-lang-client-* project — fix: list the Gemini projects under projects: in the profile, or set account: to the login that owns them", a.Login())
		}
	}
	a.mu.Lock()
	a.projects = out
	a.mu.Unlock()
	return out, nil
}

// activeIDs are the project ids to read.
func (a *API) activeIDs(ctx context.Context) ([]string, error) {
	ps, err := a.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, p := range ps {
		if !p.Archived {
			ids = append(ids, p.ID)
		}
	}
	return ids, nil
}
