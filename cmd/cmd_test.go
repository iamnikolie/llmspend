package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	testOpenAIKey    = "sk-admin-test-openai-0000"
	testAnthropicKey = "sk-ant-admin01-test-anthropic-0000"
)

// fakeAPI serves the recorded fixtures in testdata and fails the test on
// anything but an authenticated GET.
type fakeAPI struct {
	t    *testing.T
	mu   sync.Mutex
	seen []string // "path?query"
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	if r.Method != http.MethodGet {
		f.t.Errorf("non-GET request: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var file string
	switch {
	case strings.HasPrefix(r.URL.Path, "/openai/v1/"):
		if r.Header.Get("Authorization") != "Bearer "+testOpenAIKey {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("openai-organization", "org-test0000")
		p := strings.TrimPrefix(r.URL.Path, "/openai/v1/organization/")
		switch {
		case p == "usage/completions" && r.URL.Query().Get("page") == "page_test_2":
			file = "openai/usage_completions_page2.json"
		case p == "usage/completions":
			file = "openai/usage_completions.json"
		case strings.HasPrefix(p, "projects/") && strings.HasSuffix(p, "/api_keys"):
			file = "openai/api_keys_" + strings.Split(p, "/")[1] + ".json"
		case strings.HasPrefix(p, "projects/") && strings.HasSuffix(p, "/spend_limit"):
			file = "openai/spend_limit_" + strings.Split(p, "/")[1] + ".json"
		default:
			file = "openai/" + strings.ReplaceAll(p, "/", "_") + ".json"
		}
	case strings.HasPrefix(r.URL.Path, "/anthropic/v1/"):
		if r.Header.Get("x-api-key") == "sk-ant-admin01-individual-0000" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"type":"error","error":{"type":"permission_error","message":"The Admin API is unavailable for individual accounts."}}`))
			return
		}
		if r.Header.Get("x-api-key") != testAnthropicKey || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/anthropic/v1/organizations/")
		switch p {
		case "usage_report/messages":
			file = "anthropic/usage_messages.json"
		case "usage_report/claude_code":
			if r.URL.Query().Get("starting_at") != "2026-10-26" {
				w.Write([]byte(`{"data":[],"has_more":false,"next_page":null}`))
				return
			}
			file = "anthropic/claude_code.json"
		default:
			file = "anthropic/" + p + ".json"
		}
	}
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"not found","type":"not_found"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func (f *fakeAPI) requests(substr string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.seen {
		if strings.Contains(s, substr) {
			out = append(out, s)
		}
	}
	return out
}

func setup(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("LLMSPEND_HOME", t.TempDir())
	t.Setenv("LLMSPEND_CONFIG", "")
	t.Setenv("OPENAI_ADMIN_KEY", "")
	t.Setenv("ANTHROPIC_ADMIN_KEY", "")
	c := &config.Config{Orgs: []config.Org{
		{Name: "oa", Provider: config.OpenAI, AdminKey: testOpenAIKey, OrgID: "org-test0000", BaseURL: srv.URL + "/openai/v1"},
		{Name: "an", Provider: config.Anthropic, AdminKey: testAnthropicKey, BaseURL: srv.URL + "/anthropic/v1"},
	}}
	if _, err := config.Save("default", c); err != nil {
		t.Fatal(err)
	}
	now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = func() time.Time { return time.Now().UTC() } })
	return f
}

func resetFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			var def []string
			if d := strings.Trim(f.DefValue, "[]"); d != "" {
				def = strings.Split(d, ",")
			}
			_ = sv.Replace(def)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	})
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetFlags(rootCmd)
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	})
	profile = "default"
	var out, errb bytes.Buffer
	stdout, stderr = &out, &errb
	t.Cleanup(func() { stdout, stderr = os.Stdout, os.Stderr })
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), errb.String(), err
}

func decode(t *testing.T, s string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(s), &rows); err != nil {
		t.Fatalf("not JSON rows: %v\n%s", err, s)
	}
	return rows
}

func rowWhere(rows []map[string]any, kv ...string) map[string]any {
next:
	for _, r := range rows {
		for i := 0; i+1 < len(kv); i += 2 {
			if r[kv[i]] != kv[i+1] {
				continue next
			}
		}
		return r
	}
	return nil
}

func near(t *testing.T, what string, got any, want float64) {
	t.Helper()
	g, ok := got.(float64)
	if !ok || g < want-1e-6 || g > want+1e-6 {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestUsageByModelBothProviders(t *testing.T) {
	f := setup(t)
	out, errOut, err := run(t, "usage", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	mini := rowWhere(rows, "org", "oa", "model", "gpt-4o-mini-2024-07-18")
	if mini == nil {
		t.Fatalf("no gpt-4o-mini row: %v", rows)
	}
	near(t, "mini input", mini["input"], 2_000_000) // both pages summed
	near(t, "mini requests", mini["requests"], 60)
	near(t, "mini est", mini["est_usd"], 0.375)
	near(t, "4o est", rowWhere(rows, "model", "gpt-4o-2024-08-06")["est_usd"], 0.35)
	sonnet := rowWhere(rows, "org", "an", "model", "claude-sonnet-4-5-20250929")
	near(t, "sonnet input", sonnet["input"], 3_000_000)
	near(t, "sonnet est", sonnet["est_usd"], 8.55)
	if _, ok := sonnet["requests"]; ok {
		t.Fatal("anthropic rows must not claim request counts")
	}
	if !strings.Contains(errOut, "openai/made-up-model-x") {
		t.Fatalf("unpriced model not reported: %q", errOut)
	}
	if strings.Contains(errOut, "usage not read") {
		t.Fatalf("a usage family failed: %s", errOut)
	}
	// Always grouped by model at the provider, from the snapped 7-day start.
	q := f.requests("/usage/completions")
	if len(q) != 2 || !strings.Contains(q[0], "group_by=model") || !strings.Contains(q[0], "start_time=1792540800") {
		t.Fatalf("openai usage queries: %v", q)
	}
	a := f.requests("/usage_report/messages")
	if len(a) != 1 || !strings.Contains(a[0], "group_by%5B%5D=model") || !strings.Contains(a[0], "starting_at=2026-10-21T00%3A00%3A00Z") {
		t.Fatalf("anthropic usage queries: %v", a)
	}
}

func TestUsageByKeyResolvesNames(t *testing.T) {
	setup(t)
	out, errOut, err := run(t, "usage", "--by", "key,project", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	if r := rowWhere(rows, "key", "alpha-backend", "project", "Alpha App"); r == nil {
		t.Fatalf("openai key/project names not resolved: %v", rows)
	}
	if r := rowWhere(rows, "key", "local-dev", "project", "Default"); r == nil {
		t.Fatalf("anthropic default workspace not named: %v", rows)
	}
	if r := rowWhere(rows, "key", "prod-service", "project", "Production"); r == nil {
		t.Fatalf("anthropic workspace not resolved: %v", rows)
	}
}

func TestUsageMarkdownHasTotal(t *testing.T) {
	setup(t)
	out, _, err := run(t, "usage", "-p", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "| provider | org | kind | model |") || !strings.Contains(out, "| anthropic | an | messages | claude-sonnet-4-5-20250929 |") || !strings.Contains(out, "Total: 3500000 input / 150000 output tokens, est $9.30") {
		t.Fatalf("markdown:\n%s", out)
	}
	if strings.Contains(out, "openai") {
		t.Fatal("-p anthropic leaked openai rows")
	}
}

func TestCostMonthAndModel(t *testing.T) {
	f := setup(t)
	out, errOut, err := run(t, "cost", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	near(t, "openai total", rowWhere(rows, "org", "oa")["usd"], 2.00)
	near(t, "anthropic total (cents → USD)", rowWhere(rows, "org", "an")["usd"], 7.82)
	if q := f.requests("/organization/costs"); len(q) != 1 || !strings.Contains(q[0], "start_time=1790812800") {
		t.Fatalf("month start not used: %v", q)
	}

	out, _, err = run(t, "cost", "--month", "last", "--by", "model", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows = decode(t, out)
	near(t, "gpt-4o-mini", rowWhere(rows, "model", "gpt-4o-mini-2024-07-18")["usd"], 1.65)
	near(t, "web search", rowWhere(rows, "model", "(web_search)")["usd"], 0.02)
	if q := f.requests("/organization/costs"); !strings.Contains(q[len(q)-1], "group_by=line_item") || !strings.Contains(q[len(q)-1], "end_time=1790812800") {
		t.Fatalf("last month query: %v", q)
	}
}

func TestCostByKeyAnthropicIsPerOrgError(t *testing.T) {
	setup(t)
	out, errOut, err := run(t, "cost", "--by", "key", "--json")
	if err != nil {
		t.Fatalf("one failing org must not fail the run: %v", err)
	}
	if !strings.Contains(errOut, "! an (anthropic)") || !strings.Contains(errOut, "usage --by key") {
		t.Fatalf("stderr: %s", errOut)
	}
	if rows := decode(t, out); len(rows) == 0 || rows[0]["org"] != "oa" {
		t.Fatalf("rows: %v", rows)
	}
}

func TestBudgetAndAlert(t *testing.T) {
	setup(t)
	out, errOut, err := run(t, "budget", "--projects", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	org := rowWhere(rows, "org", "oa", "scope", "org")
	near(t, "limit", org["limit"], 10)
	near(t, "left", org["left"], 8)
	near(t, "used", org["used_pct"], 20)
	if org["alerts"] != "$5.00" {
		t.Fatalf("alerts = %v", org["alerts"])
	}
	alpha := rowWhere(rows, "scope", "project Alpha App")
	near(t, "alpha used", alpha["used_pct"], 82.5)
	if beta := rowWhere(rows, "scope", "project Beta Tools"); beta == nil || beta["limit"] != "none" {
		t.Fatalf("beta = %v", beta)
	}
	if an := rowWhere(rows, "org", "an"); an == nil || !strings.Contains(an["note"].(string), "no spend-limit") {
		t.Fatalf("anthropic row = %v", an)
	}

	_, errOut, err = run(t, "budget", "--projects", "--alert-at", "80")
	if code, ok := err.(exitCode); !ok || code != 3 || !strings.Contains(errOut, "oa/Alpha App 82%") {
		t.Fatalf("alert: err=%v stderr=%s", err, errOut)
	}
}

func TestKeysAndOrgs(t *testing.T) {
	setup(t)
	out, errOut, err := run(t, "keys", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	if r := rowWhere(rows, "name", "beta-worker"); r == nil || r["kind"] != "service_account" || r["project"] != "Beta Tools" || r["org_id"] != "org-test0000" {
		t.Fatalf("beta-worker = %v", r)
	}
	if r := rowWhere(rows, "name", "llmspend"); r == nil || r["kind"] != "admin" {
		t.Fatalf("admin key = %v", r)
	}
	if r := rowWhere(rows, "name", "prod-service"); r == nil || r["project"] != "Production" || r["org_id"] != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("prod-service = %v", r)
	}
	if rowWhere(rows, "name", "old-key") != nil {
		t.Fatal("archived key shown without --all")
	}
	if !strings.Contains(out, "sk-proj-****aaaa") || strings.Contains(out, testOpenAIKey) {
		t.Fatal("hints wrong or secret leaked")
	}

	out, _, err = run(t, "orgs", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows = decode(t, out)
	if r := rowWhere(rows, "org", "an"); r["org_name"] != "Test Org" || r["status"] != "ok" {
		t.Fatalf("anthropic org = %v", r)
	}
	if r := rowWhere(rows, "org", "oa"); r["org_id"] != "org-test0000" || r["key"] != "sk-admin-…0000" {
		t.Fatalf("openai org = %v", r)
	}
}

func TestClaudeCode(t *testing.T) {
	f := setup(t)
	out, errOut, err := run(t, "claude-code", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	if len(rows) != 1 || rows[0]["user"] != "dev@example.com" {
		t.Fatalf("rows = %v", rows)
	}
	near(t, "sessions", rows[0]["sessions"], 4)
	near(t, "est", rows[0]["est_usd"], 1.75)
	if n := len(f.requests("/usage_report/claude_code")); n != 7 {
		t.Fatalf("want one call per day for 7 days, got %d", n)
	}
	if len(f.requests("/openai/")) != 0 {
		t.Fatal("claude-code must not call OpenAI")
	}

	out, _, err = run(t, "cc", "--by", "model", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows = decode(t, out)
	near(t, "haiku est", rowWhere(rows, "model", "claude-haiku-4-5-20251001")["est_usd"], 0.25)
	if _, ok := rows[0]["sessions"]; ok {
		t.Fatal("per-model rows must not repeat per-record sessions")
	}
}

func TestBadKeyIsReported(t *testing.T) {
	setup(t)
	c, _ := config.Load("default")
	c.Orgs[0].AdminKey = "sk-admin-wrong"
	if _, err := config.Save("default", c); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, "usage", "-p", "openai")
	if err == nil || !strings.Contains(errOut, "HTTP 401") || !strings.Contains(errOut, "Incorrect API key") {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	_, _, err = run(t, "orgs")
	if code, ok := err.(exitCode); !ok || code != 1 {
		t.Fatalf("orgs should exit 1 on a bad key: %v", err)
	}
}

func TestConfigAddShowRm(t *testing.T) {
	setup(t)
	stdin = strings.NewReader("sk-ant-api03-notadmin-123456\n")
	t.Cleanup(func() { stdin = os.Stdin })
	_, errOut, err := run(t, "config", "add", "extra", "--provider", "claude")
	if err != nil || !strings.Contains(errOut, "does not look like an Anthropic Admin API key") {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	out, _, err := run(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "notadmin") || !strings.Contains(out, `"extra"`) {
		t.Fatalf("show leaked a key or lost the org: %s", out)
	}
	if _, _, err := run(t, "config", "rm", "extra"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "config", "rm", "extra"); err == nil {
		t.Fatal("second rm should fail")
	}
}

func TestPricesAndSkill(t *testing.T) {
	setup(t)
	out, _, err := run(t, "prices", "gpt-4o-mini", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if rows := decode(t, out); len(rows) != 1 || rows[0]["model"] != "gpt-4o-mini" {
		t.Fatalf("rows = %v", rows)
	}
	out, _, err = run(t, "skill")
	if err != nil || !strings.HasPrefix(out, "# llmspend") {
		t.Fatalf("skill: %v %q", err, out[:min(len(out), 40)])
	}
}

func TestPricesRefreshFromFakePages(t *testing.T) {
	setup(t)
	pages := map[string]string{"/openai": "openai-pricing.md", "/anthropic": "anthropic-pricing.md"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, f := range pages {
			if strings.HasPrefix(r.URL.Path, prefix) {
				b, _ := os.ReadFile(filepath.Join("..", "internal", "prices", "testdata", f))
				w.Write(b)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := httpClient
	httpClient = &http.Client{Transport: rewrite{srv.URL}}
	defer func() { httpClient = old }()
	out, errOut, err := run(t, "prices", "refresh", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	if rows := decode(t, out); len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("LLMSPEND_HOME"), "prices.json")); err != nil {
		t.Fatal("prices.json not written")
	}
}

// rewrite sends every request to the test server, keeping the path's
// provider hint.
type rewrite struct{ base string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	prefix := "/openai"
	if strings.Contains(req.URL.Host, "claude") {
		prefix = "/anthropic"
	}
	u := r.base + prefix + req.URL.Path
	nr, _ := http.NewRequestWithContext(req.Context(), req.Method, u, nil)
	return http.DefaultTransport.RoundTrip(nr)
}

func TestTimeParsing(t *testing.T) {
	setup(t)
	if s, _ := parseSince("7d"); !s.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("7d = %v", s)
	}
	if s, _ := parseSince("24h"); !s.Equal(time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("24h = %v", s)
	}
	if u, _ := parseUntil("2026-10-01"); !u.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("until = %v", u)
	}
	from, to, label, err := monthRange("2026-09")
	if err != nil || from.Month() != 9 || to.Month() != 10 || label != "2026-09" {
		t.Fatalf("month = %v %v %q %v", from, to, label, err)
	}
	if _, to, label, _ := monthRange(""); !to.IsZero() || !strings.Contains(label, "month to date") {
		t.Fatalf("current month = %v %q", to, label)
	}
	if _, err := parseSince("yesterday"); err == nil {
		t.Fatal("want error")
	}
}

func TestUsageEveryOpenAIKind(t *testing.T) {
	f := setup(t)
	out, errOut, err := run(t, "usage", "-p", "openai", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	for _, ep := range []string{"completions", "embeddings", "moderations", "decisions", "images", "audio_speeches",
		"audio_transcriptions", "web_search_calls", "file_search_calls", "code_interpreter_sessions", "vector_stores"} {
		if len(f.requests("/organization/usage/"+ep+"?")) == 0 {
			t.Errorf("%s not read", ep)
		}
	}
	emb := rowWhere(rows, "kind", "embeddings", "model", "text-embedding-3-small")
	near(t, "embeddings input", emb["input"], 50_000_000)
	near(t, "embeddings est", emb["est_usd"], 1.00)
	near(t, "decisions est", rowWhere(rows, "kind", "decisions")["est_usd"], 0.10)
	near(t, "free moderation", rowWhere(rows, "kind", "moderations")["est_usd"], 0)
	vs := rowWhere(rows, "kind", "vector_stores")
	if vs["unit"] != "bytes" {
		t.Fatalf("vector stores = %v", vs)
	}
	near(t, "stored bytes are a peak, not a sum", vs["quantity"], 2097152)
	near(t, "transcription seconds", rowWhere(rows, "kind", "transcription")["quantity"], 95)
	if _, ok := rowWhere(rows, "kind", "images")["est_usd"]; ok {
		t.Fatal("non-token kinds must not carry a token estimate")
	}
	if strings.Contains(errOut, "omni-moderation") || strings.Contains(errOut, "gpt-image") {
		t.Fatalf("free or non-token models reported as unpriced: %s", errOut)
	}
	ci := f.requests("/usage/code_interpreter_sessions")[0]
	if strings.Contains(ci, "group_by=model") {
		t.Fatalf("unsupported group_by sent: %s", ci)
	}
}

func TestUsageKindFilterAndKeySplit(t *testing.T) {
	f := setup(t)
	out, _, err := run(t, "usage", "--kind", "embeddings", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := decode(t, out)
	if len(rows) != 1 || rows[0]["kind"] != "embeddings" {
		t.Fatalf("rows = %v", rows)
	}
	if len(f.requests("/usage/completions")) != 0 || len(f.requests("/usage_report/messages")) != 0 {
		t.Fatal("--kind embeddings read other families")
	}
	out, _, err = run(t, "usage", "-p", "openai", "--kind", "code_interpreter,file_search", "--by", "key", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows = decode(t, out)
	if r := rowWhere(rows, "kind", "code_interpreter"); r == nil || r["key"] != "(not split by key)" {
		t.Fatalf("code interpreter row = %v", r)
	}
	if r := rowWhere(rows, "kind", "file_search"); r == nil || r["key"] != "alpha-backend" {
		t.Fatalf("file search row = %v", r)
	}
	if _, _, err := run(t, "usage", "--kind", "telepathy"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestAnthropicOrgOnlyErrors(t *testing.T) {
	setup(t)
	c, _ := config.Load("default")
	c.Orgs[1].AdminKey = "sk-ant-admin01-individual-0000"
	if _, err := config.Save("default", c); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, "usage", "-p", "anthropic")
	if err == nil {
		t.Fatal("want failure")
	}
	line := strings.TrimSpace(errOut)
	if strings.Count(line, "\n") != 0 || !strings.Contains(line, "organization accounts") ||
		!strings.Contains(line, "fix: set up an organization at platform.claude.com/settings/organization") ||
		!strings.Contains(line, "individual accounts") {
		t.Fatalf("stderr: %q", errOut)
	}

	c.Orgs[1].AdminKey = "sk-ant-api03-regular-key-0000"
	if _, err := config.Save("default", c); err != nil {
		t.Fatal(err)
	}
	_, errOut, _ = run(t, "cost", "-p", "anthropic")
	if !strings.Contains(errOut, "this is not an Admin API key") || !strings.Contains(errOut, "platform.claude.com/settings/admin-keys") {
		t.Fatalf("stderr: %q", errOut)
	}
}
