// Package config stores llmspend profiles: ~/.llmspend/<profile>/config.yaml
// (or $LLMSPEND_HOME/<profile>/config.yaml) listing one admin key per
// provider org.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Providers.
const (
	OpenAI    = "openai"
	Anthropic = "anthropic"
	Gemini    = "gemini"
)

// DefaultProfile is used when neither --config nor LLMSPEND_CONFIG names one.
const DefaultProfile = "default"

// Default API roots. LLMSPEND_OPENAI_BASE_URL / LLMSPEND_ANTHROPIC_BASE_URL /
// LLMSPEND_GEMINI_BASE_URL override them (tests, proxies); an org's base_url
// beats both. Gemini has no single root: its base URL, when set, prefixes
// /monitoring, /crm and /bigquery instead of the googleapis.com hosts.
const (
	OpenAIBaseURL    = "https://api.openai.com/v1"
	AnthropicBaseURL = "https://api.anthropic.com/v1"
)

// Org is one provider organization reached with one admin key. A Gemini
// org has no key: it is a set of Google Cloud projects read with a gcloud
// login.
type Org struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	AdminKey string `yaml:"admin_key"`
	OrgID    string `yaml:"org_id,omitempty"`
	BaseURL  string `yaml:"base_url,omitempty"`
	// WorkspaceID is sent as anthropic-workspace-id, for a personal key the
	// API asks to pin to a workspace. Empty means no header (the default).
	WorkspaceID string `yaml:"workspace_id,omitempty"`
	// Gemini: the gcloud account whose token reads the projects (empty =
	// Application Default Credentials), the project ids (empty = every
	// gen-lang-client-* project the account sees) and the Cloud Billing
	// export table (project.dataset.table) once billing export is on.
	Account      string   `yaml:"account,omitempty"`
	Projects     []string `yaml:"projects,omitempty"`
	BillingTable string   `yaml:"billing_table,omitempty"`
	// QuotaProject, when set, is sent as x-goog-user-project so Google
	// bills the calls' quota to it instead of gcloud's shared client project.
	QuotaProject string `yaml:"quota_project,omitempty"`
	// FromEnv marks an org synthesized from OPENAI_ADMIN_KEY / ANTHROPIC_ADMIN_KEY.
	FromEnv bool `yaml:"-"`
}

// Config is one profile.
type Config struct {
	Orgs []Org `yaml:"orgs"`
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// ValidName rejects names that would escape the profile directory.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: use letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

// ValidProvider normalizes and checks a provider name.
func ValidProvider(p string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case OpenAI:
		return OpenAI, nil
	case Anthropic, "claude":
		return Anthropic, nil
	case Gemini, "google":
		return Gemini, nil
	}
	return "", fmt.Errorf("unknown provider %q: use openai, anthropic or gemini", p)
}

// KeyWarning returns a note about a key's kind; it never rejects one, the
// provider decides on the first call.
func KeyWarning(provider, key string) string {
	switch provider {
	case OpenAI:
		if !strings.HasPrefix(key, "sk-admin-") {
			return "this does not look like an OpenAI admin key (sk-admin-…); project keys cannot read usage or costs"
		}
	case Anthropic:
		switch {
		case strings.HasPrefix(key, "sk-ant-admin"):
		case strings.HasPrefix(key, "sk-ant-api"):
			return "personal or service-account key: the Admin API takes it only if it is not scoped to one workspace, and it acts with the linked account's permissions. It can also call the Messages API and spend money, so give it an expiration"
		default:
			return "this does not look like an Anthropic key (sk-ant-admin01-… or sk-ant-api03-…)"
		}
	}
	return ""
}

// Home is the base directory holding profiles and the shared price table.
func Home() (string, error) {
	if h := os.Getenv("LLMSPEND_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".llmspend"), nil
}

// Path returns the config file of a profile.
func Path(profile string) (string, error) {
	if profile == "" {
		profile = DefaultProfile
	}
	if err := ValidName(profile); err != nil {
		return "", err
	}
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, profile, "config.yaml"), nil
}

// Load reads a profile. A missing file is an empty config, not an error.
// OPENAI_ADMIN_KEY and ANTHROPIC_ADMIN_KEY add an org named "openai-env" /
// "anthropic-env" on top of the file.
func Load(profile string) (*Config, error) {
	path, err := Path(profile)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if e := yaml.Unmarshal(b, c); e != nil {
			return nil, fmt.Errorf("parse %s: %w", path, e)
		}
	}
	for i := range c.Orgs {
		p, err := ValidProvider(c.Orgs[i].Provider)
		if err != nil {
			return nil, fmt.Errorf("%s: org %q: %w", path, c.Orgs[i].Name, err)
		}
		c.Orgs[i].Provider = p
	}
	for _, e := range []struct{ env, provider string }{
		{"OPENAI_ADMIN_KEY", OpenAI},
		{"ANTHROPIC_ADMIN_KEY", Anthropic},
	} {
		if k := os.Getenv(e.env); k != "" {
			c.Orgs = append(c.Orgs, Org{Name: e.provider + "-env", Provider: e.provider, AdminKey: k, FromEnv: true})
		}
	}
	return c, nil
}

// Save writes the profile (dir 0700, file 0600: it holds admin keys).
// Orgs that came from the environment are not written.
func Save(profile string, c *Config) (string, error) {
	path, err := Path(profile)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	out := Config{}
	for _, o := range c.Orgs {
		if !o.FromEnv {
			out.Orgs = append(out.Orgs, o)
		}
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o600)
}

// Upsert adds o or replaces the org with the same name.
func (c *Config) Upsert(o Org) {
	for i := range c.Orgs {
		if c.Orgs[i].Name == o.Name && !c.Orgs[i].FromEnv {
			c.Orgs[i] = o
			return
		}
	}
	c.Orgs = append(c.Orgs, o)
}

// Remove deletes the named org; false when it was not there.
func (c *Config) Remove(name string) bool {
	for i := range c.Orgs {
		if c.Orgs[i].Name == name && !c.Orgs[i].FromEnv {
			c.Orgs = append(c.Orgs[:i], c.Orgs[i+1:]...)
			return true
		}
	}
	return false
}

// Select returns the orgs matching provider and names (empty = any).
func (c *Config) Select(provider string, names []string) ([]Org, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []Org
	for _, o := range c.Orgs {
		if provider != "" && o.Provider != provider {
			continue
		}
		if len(want) > 0 && !want[o.Name] {
			continue
		}
		out = append(out, o)
		delete(want, o.Name)
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for n := range want {
			missing = append(missing, n)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("no org named %s in this profile (see 'llmspend config show')", strings.Join(missing, ", "))
	}
	if len(out) == 0 {
		if len(c.Orgs) == 0 {
			return nil, errors.New("no orgs configured: run 'llmspend config add <name> --provider openai|anthropic|gemini' (see 'llmspend skill' → Setup)")
		}
		return nil, fmt.Errorf("no %s org configured in this profile", provider)
	}
	return out, nil
}

// BaseURLFor resolves the API root for an org.
func BaseURLFor(o Org) string {
	if o.BaseURL != "" {
		return o.BaseURL
	}
	if o.Provider == OpenAI {
		if v := os.Getenv("LLMSPEND_OPENAI_BASE_URL"); v != "" {
			return v
		}
		return OpenAIBaseURL
	}
	if o.Provider == Gemini {
		return os.Getenv("LLMSPEND_GEMINI_BASE_URL")
	}
	if v := os.Getenv("LLMSPEND_ANTHROPIC_BASE_URL"); v != "" {
		return v
	}
	return AnthropicBaseURL
}

// MaskKey shows only the key family and its last four characters.
func MaskKey(k string) string {
	if k == "" {
		return "not set"
	}
	if len(k) <= 12 {
		return "set"
	}
	prefix := "sk-…"
	for _, p := range []string{"sk-ant-admin01-", "sk-ant-admin", "sk-admin-", "sk-ant-", "sk-proj-"} {
		if strings.HasPrefix(k, p) {
			prefix = p + "…"
			break
		}
	}
	return prefix + k[len(k)-4:]
}
