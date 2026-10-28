package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/iamnikolie/llmspend/internal/gemini"
	"github.com/iamnikolie/llmspend/internal/prices"
)

const testGcloudToken = "test-gcloud-token"

// geminiFixture maps a Google API call to a file in testdata/gemini. It
// answers directly (and returns "") for refusals and for series or
// partitions that have no fixture: Google returns an empty object there.
func geminiFixture(w http.ResponseWriter, r *http.Request) string {
	if r.Header.Get("Authorization") != "Bearer "+testGcloudToken {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`))
		return ""
	}
	p := r.URL.Path
	var file string
	switch {
	case p == "/gemini/crm/v1/projects":
		file = "gemini/projects.json"
	case strings.HasPrefix(p, "/gemini/monitoring/v3/projects/") && strings.HasSuffix(p, "/timeSeries"):
		project := strings.Split(strings.TrimPrefix(p, "/gemini/monitoring/v3/projects/"), "/")[0]
		metric := strings.TrimSuffix(strings.TrimPrefix(r.URL.Query().Get("filter"), `metric.type="generativelanguage.googleapis.com/`), `"`)
		file = "gemini/ts_" + project + "_" + strings.ReplaceAll(metric, "/", "_") + ".json"
	case p == "/gemini/bigquery/v2/projects/billing-test/datasets/billing_export/tables/gcp_billing_export_v1_TEST":
		file = "gemini/bq_table.json"
	case strings.HasPrefix(p, "/gemini/bigquery/v2/projects/billing-test/datasets/billing_export/tables/gcp_billing_export_v1_TEST$"):
		part := strings.TrimSuffix(p[strings.Index(p, "$")+1:], "/data")
		file = "gemini/bq_data_" + part + ".json"
		if r.URL.Query().Get("pageToken") == "page_test_2" {
			file = "gemini/bq_data_" + part + "_page2.json"
		}
	}
	if _, err := os.Stat(filepath.Join("testdata", file)); file == "" || err != nil {
		w.Write([]byte(`{}`))
		return ""
	}
	return file
}

// setupGemini adds a Gemini org (and a price table with Gemini rows) to the
// test profile, with gcloud swapped for a fixed token.
func setupGemini(t *testing.T, billingTable string) *fakeAPI {
	t.Helper()
	f := setup(t)
	old := gemini.Token
	gemini.Token = func(ctx context.Context, account string) (string, error) {
		if account == "nobody@example.com" {
			return "", errors.New("gemini: no gcloud token (no credentialed account) — fix: run 'gcloud auth login nobody@example.com'")
		}
		return testGcloudToken, nil
	}
	t.Cleanup(func() { gemini.Token = old })
	c, err := config.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	srv := strings.TrimSuffix(c.Orgs[0].BaseURL, "/openai/v1")
	c.Upsert(config.Org{Name: "gm", Provider: config.Gemini, Account: "me@example.com", BillingTable: billingTable, BaseURL: srv + "/gemini"})
	if _, err := config.Save("default", c); err != nil {
		t.Fatal(err)
	}
	tab, err := prices.Default()
	if err != nil {
		t.Fatal(err)
	}
	tab.Replace("gemini", prices.GeminiURL, time.Unix(0, 0), []prices.Price{
		{Provider: "gemini", Model: "gemini-3.8-flash", Input: 0.75, Output: 3.75},
		{Provider: "gemini", Model: "gemini-3-pro-image", Input: 2, Output: 12, OutputImage: 120},
		{Provider: "gemini", Model: "lyria-3-clip-preview", PerRequest: 0.04},
		{Provider: "gemini", Model: "gemini-2.5-flash-lite", Input: 0.1, Output: 0.4},
	})
	path, _ := pricesPath()
	if err := prices.Save(path, tab); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGeminiUsage(t *testing.T) {
	f := setupGemini(t, "")
	out, errOut, err := run(t, "usage", "-p", "gemini", "--since", "7d", "--by", "project,model", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	flash := rowWhere(rows, "project", "Alpha App", "model", "gemini-3.8-flash")
	if flash == nil || flash["kind"] != "generate" {
		t.Fatalf("rows = %v", rows)
	}
	near(t, "flash input", flash["input"], 2e6)
	near(t, "flash requests", flash["requests"], 10)
	near(t, "flash est", flash["est_usd"], 2*0.75+1*3.75)
	// Image output tokens at the image rate, text output at the text rate.
	near(t, "image est", rowWhere(rows, "model", "gemini-3-pro-image")["est_usd"], 0.05*2+0.01*12+0.1*120)
	// Music is priced per song (request), its tokens are not billed.
	near(t, "lyria est", rowWhere(rows, "model", "lyria-3-clip")["est_usd"], 3*0.04)
	// Free tier: counted, priced at zero.
	free := rowWhere(rows, "project", "Beta", "model", "gemini-2.5-flash-lite")
	near(t, "free input", free["input"], 1000)
	near(t, "free est", free["est_usd"], 0)
	veo := rowWhere(rows, "model", "veo-3-fast-generate")
	if veo == nil || veo["kind"] != "predict" || veo["unit"] != "requests" || veo["est_usd"] != nil {
		t.Fatalf("veo = %v", veo)
	}
	for _, want := range []string{"veo-3-fast-generate not priced", "free-tier", "estimate"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q: %s", want, errOut)
		}
	}
	// The deleted project is not read; every series of the live ones is.
	if n := len(f.requests("gen-lang-client-test-gone")); n != 0 {
		t.Fatalf("read a deleted project %d times", n)
	}
	if n := len(f.requests("/gemini/monitoring/v3/projects/gen-lang-client-test-alpha/timeSeries")); n != len(gemini.Metrics) {
		t.Fatalf("alpha: %d series calls, want %d", n, len(gemini.Metrics))
	}
}

func TestGeminiUsageKindsAndDays(t *testing.T) {
	setupGemini(t, "")
	out, _, err := run(t, "usage", "-p", "gemini", "--kind", "predict", "--by", "day,model", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := decode(t, out)
	if len(rows) != 1 || rows[0]["date"] != "2026-10-26" || rows[0]["model"] != "veo-3-fast-generate" {
		t.Fatalf("rows = %v", rows)
	}
}

func TestGeminiCostNeedsExport(t *testing.T) {
	setupGemini(t, "")
	_, errOut, err := run(t, "cost", "-p", "gemini")
	if err == nil || !strings.Contains(errOut, "billing_table") || !strings.Contains(errOut, "usage -p gemini") {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	// Other providers still print; budget shows a note row.
	out, _, err := run(t, "budget", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if r := rowWhere(decode(t, out), "provider", "gemini"); r == nil || !strings.Contains(r["note"].(string), "usage -p gemini") {
		t.Fatalf("budget = %s", out)
	}
}

func TestGeminiCostFromBillingExport(t *testing.T) {
	f := setupGemini(t, "billing-test.billing_export.gcp_billing_export_v1_TEST")
	out, errOut, err := run(t, "cost", "-p", "gemini", "--since", "2026-10-26", "--until", "2026-10-26", "--by", "project,model", "--json")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	rows := decode(t, out)
	if len(rows) != 2 {
		t.Fatalf("want the 2 Gemini rows of this org in range, got %v", rows)
	}
	in := rowWhere(rows, "model", "Gemini 3.8 Flash input tokens")
	near(t, "usd after credits", in["usd"], 4)
	near(t, "amount", in["amount"], 15.2)
	if in["currency"] != "PLN" || in["project"] != "Alpha App" {
		t.Fatalf("row = %v", in)
	}
	near(t, "image usd", rowWhere(rows, "model", "Gemini 3 Pro Image output")["usd"], 2)
	// One partition per day from the start to exportLag days after the
	// end, but not past today (2026-10-27 in tests); page 2 followed.
	got := f.requests("gcp_billing_export_v1_TEST$")
	if len(got) != 3 || !strings.Contains(got[0], "$20261026/") || !strings.Contains(got[2], "$20261027/") {
		t.Fatalf("partitions read: %v", got)
	}
	if _, _, err := run(t, "cost", "-p", "gemini", "--by", "key"); err == nil {
		t.Fatal("--by key should fail for gemini")
	}
}

func TestGeminiOrgsProjectsKeysConfig(t *testing.T) {
	setupGemini(t, "")
	out, _, err := run(t, "orgs", "-p", "gemini", "--json")
	if err != nil {
		t.Fatal(err)
	}
	r := decode(t, out)[0]
	if r["status"] != "ok" || r["key"] != "gcloud me@example.com" || !strings.Contains(r["org_id"].(string), "gen-lang-client-test-alpha") {
		t.Fatalf("orgs = %v", r)
	}
	out, _, err = run(t, "projects", "-p", "gemini", "--json")
	if err != nil || len(decode(t, out)) != 2 {
		t.Fatalf("projects: %v %s", err, out)
	}
	_, errOut, err := run(t, "keys", "-p", "gemini")
	if err != nil || !strings.Contains(errOut, "not listed") {
		t.Fatalf("keys: %v %s", err, errOut)
	}

	// config add: no key, a login; a key is refused.
	if _, _, err := run(t, "config", "add", "g2", "--provider", "google", "--account", "nobody@example.com", "--projects", "p1,p2"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = run(t, "config", "show", "--json")
	if r := rowWhere(decode(t, out), "name", "g2"); r == nil || r["key"] != "gcloud nobody@example.com" || r["org_id"] != "p1,p2" {
		t.Fatalf("show = %s", out)
	}
	if _, _, err := run(t, "config", "add", "g3", "--provider", "gemini", "--api-key", "AIza-test"); err == nil {
		t.Fatal("a gemini key should be refused")
	}
	// A login gcloud cannot use fails that org only, with the fix.
	out, _, err = run(t, "orgs", "--org", "g2", "--json")
	if err == nil || !strings.Contains(out, "gcloud auth login nobody@example.com") {
		t.Fatalf("bad login: %v %s", err, out)
	}
}
