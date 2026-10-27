package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/spf13/cobra"
)

var (
	addProvider  string
	addKey       string
	addOrgID     string
	addBaseURL   string
	addWorkspace string
)

// readKey picks the key: --api-key, else the first non-empty stdin line.
func readKey(flag string, r io.Reader) (string, error) {
	if flag != "" {
		return strings.TrimSpace(flag), nil
	}
	line, _ := bufio.NewReader(r).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("no admin key given: pass --api-key or pipe it on stdin")
	}
	return line, nil
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage admin keys: one org entry per provider org, in profiles",
}

var configAddCmd = &cobra.Command{
	Use:     "add <name>",
	Aliases: []string{"init", "set"},
	Short:   "Save an org's admin key (writes ~/.llmspend/<profile>/config.yaml, mode 0600)",
	Example: `  pbpaste | llmspend config add personal-openai --provider openai --org-id org-…
  llmspend config add team-anthropic --provider anthropic     # prompts; paste the key
  llmspend --config work config add acme --provider openai --api-key "$KEY"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := config.ValidName(name); err != nil {
			return err
		}
		p, err := config.ValidProvider(addProvider)
		if err != nil {
			return fmt.Errorf("--provider: %w", err)
		}
		if addKey == "" {
			hint := "https://platform.openai.com/settings/organization/admin-keys, Permissions: Read only"
			if p == config.Anthropic {
				hint = "admin key from platform.claude.com/settings/admin-keys, or a personal key with no workspace scope from platform.claude.com/settings/keys"
			}
			fmt.Fprintf(stderr, "%s key (%s): ", p, hint)
		}
		key, err := readKey(addKey, stdin)
		if err != nil {
			return err
		}
		if w := config.KeyWarning(p, key); w != "" {
			fmt.Fprintln(stderr, "note:", w)
		}
		c, err := config.Load(profile)
		if err != nil {
			return err
		}
		c.Upsert(config.Org{Name: name, Provider: p, AdminKey: key, OrgID: addOrgID, BaseURL: addBaseURL, WorkspaceID: addWorkspace})
		path, err := config.Save(profile, c)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Saved %s (%s, key %s) to %s\nCheck it: llmspend orgs --org %s\n", name, p, config.MaskKey(key), path, name)
		return nil
	},
}

var configRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"remove"},
	Short:   "Remove an org from the profile (the key stays valid at the provider)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.Load(profile)
		if err != nil {
			return err
		}
		if !c.Remove(args[0]) {
			return fmt.Errorf("no org named %q in profile %s", args[0], profile)
		}
		path, err := config.Save(profile, c)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed %s from %s. Revoke the key in the provider console if it is no longer needed.\n", args[0], path)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the profile's orgs with masked keys",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.Load(profile)
		if err != nil {
			return err
		}
		path, _ := config.Path(profile)
		var rows []map[string]any
		for _, o := range c.Orgs {
			src := "file"
			if o.FromEnv {
				src = "env"
			}
			rows = append(rows, map[string]any{
				"name": o.Name, "provider": o.Provider, "org_id": o.OrgID, "key": config.MaskKey(o.AdminKey),
				"base_url": config.BaseURLFor(o), "source": src,
			})
		}
		if err := emitRows(rows, []string{"name", "provider", "org_id", "key", "base_url", "source"}); err != nil {
			return err
		}
		footer("Profile %s: %s", profile, path)
		return nil
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the profile's config file path",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.Path(profile)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, path)
		return nil
	},
}

func init() {
	f := configAddCmd.Flags()
	f.StringVar(&addProvider, "provider", "", "openai or anthropic (required)")
	f.StringVar(&addKey, "api-key", "", "admin key; else read from stdin")
	f.StringVar(&addOrgID, "org-id", "", "expected org id (org-… / uuid); 'orgs' warns on mismatch")
	f.StringVar(&addBaseURL, "base-url", "", "API root override (proxies, tests)")
	f.StringVar(&addWorkspace, "workspace-id", "", "Anthropic: send anthropic-workspace-id (only if the API asks for it)")
	_ = configAddCmd.MarkFlagRequired("provider")
	configCmd.AddCommand(configAddCmd, configRmCmd, configShowCmd, configPathCmd)
	rootCmd.AddCommand(configCmd)
}
