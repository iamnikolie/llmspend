package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("LLMSPEND_HOME", t.TempDir())
	t.Setenv("OPENAI_ADMIN_KEY", "")
	t.Setenv("ANTHROPIC_ADMIN_KEY", "")
	c := &Config{}
	c.Upsert(Org{Name: "oa", Provider: OpenAI, AdminKey: "sk-admin-aaaaaaaaaaaa1111"})
	c.Upsert(Org{Name: "an", Provider: Anthropic, AdminKey: "sk-ant-admin01-bbbbbbbb2222"})
	c.Upsert(Org{Name: "oa", Provider: OpenAI, AdminKey: "sk-admin-cccccccccccc3333", OrgID: "org-test"})
	path, err := Save("default", c)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	if d, _ := os.Stat(filepath.Dir(path)); d.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", d.Mode().Perm())
	}
	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Orgs) != 2 || got.Orgs[0].AdminKey != "sk-admin-cccccccccccc3333" || got.Orgs[0].OrgID != "org-test" {
		t.Fatalf("orgs = %+v", got.Orgs)
	}
	if !got.Remove("an") || got.Remove("an") {
		t.Fatal("remove")
	}
}

func TestEnvOrgsAndSelect(t *testing.T) {
	t.Setenv("LLMSPEND_HOME", t.TempDir())
	t.Setenv("OPENAI_ADMIN_KEY", "sk-admin-envenvenvenv0000")
	t.Setenv("ANTHROPIC_ADMIN_KEY", "")
	c, err := Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Orgs) != 1 || c.Orgs[0].Name != "openai-env" || !c.Orgs[0].FromEnv {
		t.Fatalf("orgs = %+v", c.Orgs)
	}
	if _, err := c.Select(Anthropic, nil); err == nil {
		t.Fatal("want error for missing provider")
	}
	if _, err := c.Select("", []string{"nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
	path, _ := Save("work", c)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "envenv") {
		t.Fatal("env key written to disk")
	}
}

func TestValidation(t *testing.T) {
	if ValidName("../x") == nil || ValidName("ok-name_1.2") != nil {
		t.Fatal("ValidName")
	}
	if p, err := ValidProvider("google"); err != nil || p != Gemini {
		t.Fatalf("google = %q %v", p, err)
	}
	if p, err := ValidProvider("Claude"); err != nil || p != Anthropic {
		t.Fatal("ValidProvider claude")
	}
	if KeyWarning(OpenAI, "sk-proj-x") == "" || KeyWarning(OpenAI, "sk-admin-x") != "" {
		t.Fatal("KeyWarning openai")
	}
	if !strings.Contains(KeyWarning(Anthropic, "sk-ant-api03-x"), "spend money") || KeyWarning(Anthropic, "sk-ant-admin01-x") != "" || KeyWarning(Anthropic, "xyz") == "" {
		t.Fatal("KeyWarning anthropic")
	}
	if got := MaskKey("sk-admin-abcdefghijklmnop1234"); got != "sk-admin-…1234" {
		t.Fatalf("mask = %s", got)
	}
}
