package cmd

import (
	"context"
	"fmt"

	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/spf13/cobra"
)

// orgID asks the provider which org the key belongs to: Anthropic's
// /organizations/me, OpenAI's /v1/me matched by the org slug. Falls back to
// the org_id in the config, then to the OpenAI slug.
func (t target) orgID(ctx context.Context) string {
	if t.an != nil {
		if o, err := t.an.Me(ctx); err == nil {
			return o.ID
		}
		return t.org.OrgID
	}
	if w, err := t.oa.Who(ctx); err == nil {
		if w.ID != "" {
			return w.ID
		}
		if t.org.OrgID != "" {
			return t.org.OrgID
		}
		return w.Slug
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
				w, err := t.oa.Who(ctx)
				switch {
				case err != nil:
					row["status"] = "error: " + firstLine(err.Error())
					bad++
				case w.ID != "":
					row["org_id"], row["org_name"] = w.ID, w.Title
				default:
					row["org_id"] = t.org.OrgID
					row["status"] = "ok (org id not resolved; slug " + w.Slug + ")"
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
