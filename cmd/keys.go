package cmd

import (
	"sort"

	"github.com/spf13/cobra"
)

var keysArchived bool

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Every API key the admin APIs list, with its project/workspace and org",
	Long: "OpenAI: project keys (one call per project) and admin keys. Anthropic: API keys in\n" +
		"every status with their workspace; the Admin API does not list Admin keys.\n" +
		"Only the providers' redacted hints are shown, never a secret.",
	Example: `  llmspend keys
  llmspend keys -p openai --fields org,project,name,hint,last_used`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ts, err := targets("")
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()
		var rows []map[string]any
		err = eachOrg(ts, func(t target) error {
			ps, err := t.projects(ctx)
			if err != nil {
				return err
			}
			ks, err := t.keys(ctx, ps)
			if err != nil {
				return err
			}
			orgID := t.orgID(ctx)
			for _, k := range ks {
				if !keysArchived && (k.Status == "archived" || k.Status == "inactive" || k.Status == "expired") {
					continue
				}
				project := k.Project
				if project == "" {
					project = k.ProjectID
				}
				rows = append(rows, map[string]any{
					"provider": t.org.Provider, "org": t.org.Name, "org_id": orgID,
					"kind": k.Kind, "project": project, "name": k.Name, "id": k.ID,
					"hint": k.Hint, "owner": k.Owner, "status": k.Status,
					"created": day(k.Created), "last_used": day(k.LastUsed),
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			for _, c := range []string{"provider", "org", "project", "name"} {
				if a[c].(string) != b[c].(string) {
					return a[c].(string) < b[c].(string)
				}
			}
			return false
		})
		return emitRows(rows, []string{"provider", "org", "kind", "project", "name", "hint", "id", "owner", "status", "created", "last_used"})
	},
}

var projectsCmd = &cobra.Command{
	Use:     "projects",
	Aliases: []string{"workspaces"},
	Short:   "OpenAI projects and Anthropic workspaces per org",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ts, err := targets("")
		if err != nil {
			return err
		}
		ctx, cancel := ctxOf(cmd)
		defer cancel()
		var rows []map[string]any
		err = eachOrg(ts, func(t target) error {
			ps, err := t.projects(ctx)
			if err != nil {
				return err
			}
			for _, p := range ps {
				if p.Archived && !keysArchived {
					continue
				}
				rows = append(rows, map[string]any{
					"provider": t.org.Provider, "org": t.org.Name, "id": p.ID,
					"name": p.Name, "status": p.Status, "created": day(p.Created),
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		return emitRows(rows, []string{"provider", "org", "name", "id", "status", "created"})
	},
}

func init() {
	keysCmd.Flags().BoolVar(&keysArchived, "all", false, "include archived, inactive and expired keys")
	projectsCmd.Flags().BoolVar(&keysArchived, "all", false, "include archived projects/workspaces")
	rootCmd.AddCommand(keysCmd, projectsCmd)
}
