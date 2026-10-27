// Package spend holds the provider-neutral records both providers are
// normalized into, so the commands can group and render them the same way.
package spend

import "time"

// Usage is usage of one kind for one time bucket and one group. ProjectID
// is an OpenAI project or an Anthropic workspace ("" = the default workspace
// or not grouped). Input counts every input token, cached and cache writes
// included, so Input means the same thing for both providers. Kinds billed
// by something other than tokens (images, audio seconds, tool calls, stored
// bytes) carry it in Quantity, named by Unit.
type Usage struct {
	Kind         string // "completions", "embeddings", … ; Anthropic: "messages"
	Start        time.Time
	Model        string
	APIKeyID     string
	ProjectID    string
	Requests     int64 // OpenAI only; Anthropic does not report request counts
	Input        int64
	CachedInput  int64 // cache reads
	CacheWrite   int64 // cache writes, all TTLs
	CacheWrite1h int64 // Anthropic: the 1-hour subset of CacheWrite
	Output       int64
	WebSearches  int64
	Quantity     int64
	Unit         string // "images", "characters", "seconds", "calls", "sessions", "bytes"
	// NoKeySplit marks kinds whose endpoint cannot group by API key
	// (code interpreter sessions, vector stores).
	NoKeySplit bool
}

// Cost is billed money for one day and one group, in USD.
type Cost struct {
	Start     time.Time
	ProjectID string
	APIKeyID  string
	LineItem  string // OpenAI line_item, Anthropic description
	Model     string
	USD       float64
}

// Project is an OpenAI project or an Anthropic workspace.
type Project struct {
	ID       string
	Name     string
	Status   string
	Created  time.Time
	Archived bool
}

// Key is an API key as the admin API describes it (never the secret).
type Key struct {
	ID        string
	Name      string
	Hint      string // redacted value from the provider
	Kind      string // "project", "admin", "service_account", "workspace"…
	ProjectID string
	Project   string
	Owner     string
	Status    string
	Created   time.Time
	LastUsed  time.Time
}
