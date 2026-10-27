# llmspend

[![CI](https://github.com/iamnikolie/llmspend/actions/workflows/ci.yml/badge.svg)](https://github.com/iamnikolie/llmspend/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/iamnikolie/llmspend.svg)](https://pkg.go.dev/github.com/iamnikolie/llmspend)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

What your OpenAI and Anthropic orgs used and spent, from the shell. One
binary for both providers, read with admin keys, read-only.

```bash
llmspend usage --since 7d --by model     # every usage kind per model, with an estimated cost
llmspend usage --kind embeddings --by key
llmspend cost --month                    # billed cost, month to date, per org
llmspend cost --month last --by model
llmspend budget --alert-at 80            # spend limit vs month to date; exit 3 when close
llmspend keys                            # which key belongs to which project and org
llmspend prices gpt-4o                   # local price table; 'prices refresh' updates it
llmspend claude-code --since 30d         # Claude Code analytics per user
```

Built for coding agents and cron as much as for people: Markdown tables on
stdout, notes and per-org errors on stderr, `--json` for scripts, and
`llmspend skill` prints the full reference for an agent to read. One static
Go binary.

Sibling of [`mcpcli`](https://github.com/iamnikolie/mcpcli),
[`exa-cli`](https://github.com/iamnikolie/exa-cli) and
[`svidoq`](https://github.com/iamnikolie/svidoq): same doctrine, plain text
on stdout, nothing costs context until it is called.

## Why

Finding out what one app spent meant screenshots of two consoles, the
pricing pages and a guess at which org a key lives in. The data is all in
the providers' admin APIs:

| | OpenAI | Anthropic |
| --- | --- | --- |
| Usage | [Usage API](https://platform.openai.com/docs/api-reference/usage), all 11 families | [Usage report](https://platform.claude.com/docs/en/manage-claude/usage-cost-api) (Messages) |
| Billed cost | Costs API | Cost report |
| Spend limit / alerts | org and project spend limits and spend alerts | Enterprise only, not for Console orgs |
| Keys → projects | projects, project keys, admin keys | API keys, workspaces |
| Claude Code | — | [Claude Code Analytics API](https://platform.claude.com/docs/en/manage-claude/claude-code-analytics-api), Console-billed use only |
| Who can read it | any org, admin key | organization accounts only, admin key |
| Prepaid credit balance | not in the API | not in the API |

`llmspend` reads them, normalizes the two shapes into one set of rows, and
names projects and keys instead of printing ids.

**Read-only by construction.** The HTTP client can only send `GET`: there is
no code path that creates, changes or deletes anything at the provider, and
none that calls a model.

## Install

**Homebrew:**

```bash
brew trust iamnikolie/tap   # Homebrew 6 refuses untrusted third-party taps
brew tap iamnikolie/tap
brew install iamnikolie/tap/llmspend
```

**Prebuilt binary** — download the archive for your platform from
[Releases](https://github.com/iamnikolie/llmspend/releases), check it against
`checksums.txt`, then:

```bash
tar xzf llmspend_*_darwin_arm64.tar.gz
sudo mv llmspend /usr/local/bin/
```

**With Go** (1.26+):

```bash
go install github.com/iamnikolie/llmspend@latest
```

**From source** — `make install` symlinks the binary, so a later `make build`
updates the installed CLI:

```bash
git clone https://github.com/iamnikolie/llmspend.git
cd llmspend
make install        # symlink → ~/.local/bin/llmspend
```

## Keys

One **admin key per org**. Regular API keys cannot read usage or cost.

- **OpenAI** — an org Owner opens
  [Settings → Organization → Admin keys](https://platform.openai.com/settings/organization/admin-keys),
  creates a key and copies the `sk-admin-…` secret.
- **Anthropic** — an org admin opens
  [Settings → Admin keys](https://platform.claude.com/settings/admin-keys),
  creates a key and copies the `sk-ant-admin01-…` secret. The Admin API needs
  an organization; individual accounts do not have it.

```bash
pbpaste | llmspend config add personal-openai --provider openai --org-id org-…
pbpaste | llmspend config add team-anthropic --provider anthropic
llmspend orgs          # checks every key and shows the org it belongs to
llmspend config show   # keys masked
```

Keys live in `~/.llmspend/<profile>/config.yaml` (mode 0600, directory 0700).
`--config work` (or `LLMSPEND_CONFIG=work`) switches to another profile, so
personal and work orgs never mix. `OPENAI_ADMIN_KEY` and
`ANTHROPIC_ADMIN_KEY` add a temporary org without touching the file.

## Commands

| Command | What it shows |
| --- | --- |
| `usage [--since 7d] [--by model,key,project,day] [--kind K] [--bucket 1d\|1h]` | Every usage kind with a `kind` column: tokens (input / cached / cache-write / output, OpenAI request counts, `est_usd`) or a quantity and unit (images, characters, seconds, calls, sessions, bytes) |
| `cost [--month current\|last\|YYYY-MM] [--since 7d] [--by day,project,model,line,key]` | Billed USD; month to date by default, total under the table |
| `budget [--projects] [--alert-at N]` | Month-to-date cost next to spend limits and alerts; exit 3 when a limit is N% used |
| `keys [--all]` | Every key the admin API lists, with project/workspace, org id, redacted hint, owner, last use |
| `projects [--all]` | OpenAI projects and Anthropic workspaces |
| `orgs` | Each admin key checked against its provider, with the org id (and Anthropic org name) |
| `claude-code [--since 7d] [--by user,model,day,terminal]` | Sessions, commits, PRs, lines, tokens and Anthropic's cost estimate |
| `prices [model]` / `prices refresh` | The local price table; refresh re-reads both public pricing pages |
| `skill` | The agent reference |

Every command runs across all orgs in the profile; `-p openai|anthropic` and
`--org a,b` narrow it. A failing org prints `! <org> (<provider>): …` on stderr
and the others still answer.

`--json` prints normalized rows; `--format csv|tsv|table`, `--fields a,b`.
Times are UTC; `--since` takes `7d`, `24h`, `2w`, `YYYY-MM-DD` or RFC 3339.

### Usage kinds

OpenAI reports usage per family; `usage` reads all of them and tags each row:
`completions` (Chat Completions and Responses), `embeddings`, `moderations`,
`decisions`, `images`, `speech`, `transcription`, `web_search`,
`file_search`, `code_interpreter`, `vector_stores`. Anthropic's report is one
kind, `messages`. `--kind embeddings,completions` narrows the read. Token
kinds are priced from the local table; the others show a quantity and unit
and leave the money to `cost`.

### Estimates vs bills

`usage` prices tokens with the local table at standard rates, so it can split
by key and by hour, which the cost reports cannot. `cost` is what the provider
bills. Batch and priority discounts, long-context rates and tool fees make the
two differ; trust `cost` for money and `usage` for where it went.

The price table ships inside the binary and is replaced by
`~/.llmspend/prices.json` after `llmspend prices refresh`, which parses the
providers' public pricing pages (Markdown). A refresh that finds no prices
leaves the table as it was.

### Limits

What the provider APIs do not give, stated plainly:

- **No prepaid credit balance** on either provider. `budget` shows the
  billing page link instead; `--alert-at` against an OpenAI spend limit is the
  closest warning the APIs allow.
- **Anthropic's Admin API is for organization accounts only.** An individual
  account gets a one-line error with the fix: set up an organization at
  [platform.claude.com/settings/organization](https://platform.claude.com/settings/organization),
  then create an admin key.
- **Anthropic spend limits are Enterprise-only.** Console orgs get cost, not
  limits.
- **Claude Code analytics covers Console-billed use only** (API keys,
  Team/Enterprise seats), never Pro/Max subscriptions.
- **OpenAI spend limits and spend alerts are readable**, for the org and per
  project; `budget` compares month-to-date cost against them.
- Anthropic's cost report has no per-key split and leaves Priority Tier out.
- Usage data lags about five minutes; Claude Code analytics about an hour.

## Development

```bash
make build      # ./llmspend
make test       # go test ./...  (fixture server, no network)
make lint       # golangci-lint
make vet
make fmt
make prices     # refresh the embedded price snapshot
```

Tests run against a local server replaying recorded API shapes from
`cmd/testdata/`; the fake server fails any request that is not an
authenticated `GET`.

## License

MIT — see [LICENSE](LICENSE).
