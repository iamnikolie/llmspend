package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/spf13/cobra"
)

// orgID asks the provider which org the key belongs to: Anthropic's
// /organizations/me, or the openai-organization header on a cheap list
// call. Falls back to the org_id in the config.
func (t target) orgID(ctx context.Context) string {
	if t.an != nil {
		if o, err := t.an.Me(ctx); err == nil {
			return o.ID
		}
		return t.org.OrgID
	}
	if t.oa.Org == "" {
		if r, err := t.oa.C.Get(ctx, "/organization/projects", url.Values{"limit": {"1"}}); err == nil {
			t.oa.Org = r.Header.Get("openai-organization")
		}
	}
	if t.oa.Org != "" {
		return t.oa.Org
	}
	return t.org.OrgID
}

var orgsCmd = &cobra.Command{
	Use:     "orgs",
	Aliases: []string{"whoami", "check"},
	Short:   "Check each configured admin key and show the org it belongs to",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ts, err := targets("")
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()
		var rows []map[string]any
		bad := 0
		for _, t := range ts {
			row := map[string]any{"provider": t.org.Provider, "org": t.org.Name, "key": config.MaskKey(t.org.AdminKey), "status": "ok"}
			if t.org.FromEnv {
				row["org"] = t.org.Name + " (env)"
			}
			if t.an != nil {
				o, err := t.an.Me(ctx)
				if err != nil {
					row["status"] = "error: " + firstLine(err.Error())
					bad++
				} else {
					row["org_id"], row["org_name"] = o.ID, o.Name
				}
			} else {
				r, err := t.oa.C.Get(ctx, "/organization/projects", url.Values{"limit": {"1"}})
				if err != nil {
					row["status"] = "error: " + firstLine(err.Error())
					bad++
				} else if id := r.Header.Get("openai-organization"); id != "" {
					row["org_id"] = id
				} else {
					row["org_id"] = t.org.OrgID
				}
			}
			if t.org.OrgID != "" && row["org_id"] != nil && row["org_id"] != t.org.OrgID {
				row["status"] = fmt.Sprintf("ok, but config says org_id %s", t.org.OrgID)
			}
			rows = append(rows, row)
		}
		if err := emitRows(rows, []string{"provider", "org", "org_id", "org_name", "key", "status"}); err != nil {
			return err
		}
		if bad > 0 {
			return exitCode(1)
		}
		return nil
	},
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

func init() {
	rootCmd.AddCommand(orgsCmd)
}
