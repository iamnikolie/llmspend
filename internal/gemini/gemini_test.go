package gemini

import (
	"testing"

	"github.com/iamnikolie/llmspend/internal/client"
)

func TestNewAndQuotaProject(t *testing.T) {
	a := New(&client.Client{BaseURL: "http://x/gemini"}, "me@example.com", nil, "")
	if a.Monitoring.BaseURL != "http://x/gemini/monitoring/v3" || a.BigQuery.BaseURL != "http://x/gemini/bigquery/v2" || a.Monitoring.RetryWait == 0 {
		t.Fatalf("clients = %+v %+v", a.Monitoring, a.BigQuery)
	}
	if a.CRM.Headers != nil {
		t.Fatal("no quota header by default")
	}
	a.UseQuotaProject("quota-test")
	for _, c := range []*client.Client{a.Monitoring, a.CRM, a.BigQuery} {
		if c.Headers["x-goog-user-project"] != "quota-test" {
			t.Fatalf("headers = %v", c.Headers)
		}
	}
	if p := New(&client.Client{}, "", nil, ""); p.Monitoring.BaseURL != MonitoringURL || p.Login() != "gcloud ADC" {
		t.Fatalf("defaults = %+v", p.Monitoring)
	}
}

func TestSplitTable(t *testing.T) {
	for _, s := range []string{"p.d.t", "p:d.t"} {
		if p, d, tb, err := splitTable(s); err != nil || p != "p" || d != "d" || tb != "t" {
			t.Fatalf("%s → %s %s %s %v", s, p, d, tb, err)
		}
	}
	if _, _, _, err := splitTable("d.t"); err == nil {
		t.Fatal("two parts accepted")
	}
}
