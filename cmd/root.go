package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/iamnikolie/llmspend/internal/anthropic"
	"github.com/iamnikolie/llmspend/internal/client"
	"github.com/iamnikolie/llmspend/internal/config"
	"github.com/iamnikolie/llmspend/internal/gemini"
	"github.com/iamnikolie/llmspend/internal/openai"
	"github.com/spf13/cobra"
)

// version is stamped at link time by the Makefile and by GoReleaser
// (-X github.com/iamnikolie/llmspend/cmd.version=…).
var version = "dev"

func buildVersion() string {
	v := version
	if info, ok := debug.ReadBuildInfo(); ok {
		// go install …@v1.2.3 is not stamped but knows its module version.
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = strings.TrimPrefix(info.Main.Version, "v")
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				rev := s.Value
				if len(rev) > 12 {
					rev = rev[:12]
				}
				v += " (" + rev + ")"
			}
		}
	}
	return v
}

var (
	profile      string
	providerFlag string
	orgFlags     []string
	jsonOutput   bool
	outputFormat string
	fieldsFlag   []string
	timeout      time.Duration
	verbose      bool
)

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
	stdin  io.Reader = os.Stdin

	// httpClient is swapped in tests.
	httpClient = http.DefaultClient
)

var rootCmd = &cobra.Command{
	Use:   "llmspend",
	Short: "Read-only OpenAI, Anthropic and Gemini usage, cost, budgets and keys from the shell",
	Long: "llmspend — what your OpenAI and Anthropic orgs and Gemini projects used and spent,\n" +
		"read with admin keys (Gemini: a gcloud login).\n\n" +
		"  llmspend usage --since 7d --by model     tokens per model, with estimated cost\n" +
		"  llmspend cost --month                    billed cost, month to date\n" +
		"  llmspend budget                          spend limit vs month to date\n" +
		"  llmspend keys                            which key belongs to which project and org\n" +
		"  llmspend prices                          local price table ('prices refresh' updates it)\n\n" +
		"It only reads: no generation, no changes on the provider side.\n" +
		"Run 'llmspend skill' for the full agent reference.",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		switch outputFormat {
		case "", "md", "table", "json", "csv", "tsv":
		default:
			return fmt.Errorf("--format must be one of md, table, json, csv, tsv")
		}
		if providerFlag != "" {
			p, err := config.ValidProvider(providerFlag)
			if err != nil {
				return err
			}
			providerFlag = p
		}
		client.UserAgent = "llmspend/" + version
		return nil
	},
}

// Execute runs the CLI and exits non-zero on error.
func Execute() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		if code, ok := err.(exitCode); ok {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// exitCode ends the run with a status and no extra message.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func init() {
	pf := rootCmd.PersistentFlags()
	def := os.Getenv("LLMSPEND_CONFIG")
	if def == "" {
		def = config.DefaultProfile
	}
	pf.StringVar(&profile, "config", def, "profile name (subdirectory of ~/.llmspend/; env LLMSPEND_CONFIG)")
	pf.StringVarP(&providerFlag, "provider", "p", "", "only this provider: openai, anthropic or gemini")
	pf.StringSliceVar(&orgFlags, "org", nil, "only these orgs by config name (comma-separated)")
	pf.BoolVar(&jsonOutput, "json", false, "JSON output (same as --format json)")
	pf.StringVar(&outputFormat, "format", "", "output format: md (default), table, json, csv, tsv")
	pf.StringSliceVar(&fieldsFlag, "fields", nil, "comma-separated columns to keep")
	pf.DurationVar(&timeout, "timeout", 2*time.Minute, "overall timeout per command (0 disables)")
	pf.BoolVar(&verbose, "verbose", false, "log HTTP requests (method, URL, status) to stderr")

	rootCmd.Version = buildVersion()
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the llmspend version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(stdout, "llmspend version %s\n", buildVersion())
		return nil
	},
}

func ctxOf(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// target is one configured org with a client for its provider.
type target struct {
	org config.Org
	oa  *openai.API
	an  *anthropic.API
	gm  *gemini.API
}

func newClient(o config.Org) *client.Client {
	c := &client.Client{BaseURL: config.BaseURLFor(o), Key: o.AdminKey, Verbose: verbose, HTTP: httpClient}
	if o.WorkspaceID != "" && o.Provider == config.Anthropic {
		c.Headers = map[string]string{"anthropic-workspace-id": o.WorkspaceID}
	}
	return c
}

// targets loads the profile and returns the orgs selected by --provider and
// --org; only, when set, restricts to one provider.
func targets(only string) ([]target, error) {
	c, err := config.Load(profile)
	if err != nil {
		return nil, err
	}
	p := providerFlag
	if only != "" {
		if p != "" && p != only {
			return nil, fmt.Errorf("this command reads %s only", only)
		}
		p = only
	}
	orgs, err := c.Select(p, orgFlags)
	if err != nil {
		return nil, err
	}
	out := make([]target, 0, len(orgs))
	for _, o := range orgs {
		t := target{org: o}
		switch o.Provider {
		case config.OpenAI:
			t.oa = openai.New(newClient(o))
		case config.Gemini:
			t.gm = gemini.New(newClient(o), o.Account, o.Projects, o.BillingTable)
			t.gm.Now = now
		default:
			t.an = anthropic.New(newClient(o))
		}
		out = append(out, t)
	}
	return out, nil
}

// eachOrg runs fn per target. A failing org is reported on stderr and the
// rest still run; the command fails only when every org failed.
func eachOrg(ts []target, fn func(t target) error) error {
	failed := 0
	var last error
	for _, t := range ts {
		if err := fn(t); err != nil {
			failed++
			last = err
			fmt.Fprintf(stderr, "! %s (%s): %v\n", t.org.Name, t.org.Provider, err)
		}
	}
	if failed > 0 && failed == len(ts) {
		if len(ts) == 1 {
			return exitCode(1)
		}
		return fmt.Errorf("all %d orgs failed; last: %w", failed, last)
	}
	return nil
}
