package cmd

import (
	"context"
	"fmt"

	"github.com/iamnikolie/llmspend/internal/spend"
)

// names turns project/workspace and key ids into the names people gave them.
// Lookups are best effort: a failure is a stderr note, and ids stay ids.
type names struct {
	projects map[string]string
	keys     map[string]string
}

func (t target) projects(ctx context.Context) ([]spend.Project, error) {
	if t.oa != nil {
		return t.oa.Projects(ctx, true)
	}
	return t.an.Workspaces(ctx, true)
}

// keys lists every key the admin API shows: OpenAI project keys (one call
// per project) plus admin keys; Anthropic API keys with workspace names.
func (t target) keys(ctx context.Context, ps []spend.Project) ([]spend.Key, error) {
	byID := map[string]string{}
	for _, p := range ps {
		byID[p.ID] = p.Name
	}
	if t.oa != nil {
		var out []spend.Key
		for _, p := range ps {
			if p.Archived {
				continue
			}
			ks, err := t.oa.ProjectKeys(ctx, p)
			if err != nil {
				return nil, fmt.Errorf("keys of project %s: %w", p.ID, err)
			}
			out = append(out, ks...)
		}
		admin, err := t.oa.AdminKeys(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "! %s: admin keys not listed: %v\n", t.org.Name, err)
		}
		return append(out, admin...), nil
	}
	ks, err := t.an.Keys(ctx)
	if err != nil {
		return nil, err
	}
	users, err := t.an.Users(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "! %s: key owners not named: %v\n", t.org.Name, err)
	}
	for i := range ks {
		ks[i].Project = byID[ks[i].ProjectID]
		if n := users[ks[i].Owner]; n != "" {
			ks[i].Owner = n
		}
	}
	return ks, nil
}

func resolveNames(ctx context.Context, t target, wantProjects, wantKeys bool) names {
	n := names{projects: map[string]string{}, keys: map[string]string{}}
	if !wantProjects && !wantKeys {
		return n
	}
	ps, err := t.projects(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "! %s: project names not resolved: %v\n", t.org.Name, err)
		return n
	}
	for _, p := range ps {
		n.projects[p.ID] = p.Name
	}
	if wantKeys {
		ks, err := t.keys(ctx, ps)
		if err != nil {
			fmt.Fprintf(stderr, "! %s: key names not resolved: %v\n", t.org.Name, err)
		}
		for _, k := range ks {
			n.keys[k.ID] = k.Name
		}
	}
	return n
}

// project names a project id. Anthropic reports the default workspace as
// an empty id.
func (n names) project(t target, id string) string {
	if id == "" {
		if t.an != nil {
			return "Default"
		}
		return ""
	}
	if name := n.projects[id]; name != "" {
		return name
	}
	return id
}

func (n names) key(id string) string {
	if id == "" {
		return "(console / no key)"
	}
	if name := n.keys[id]; name != "" {
		return name
	}
	return id
}
