# llmspend

[![CI](https://github.com/iamnikolie/llmspend/actions/workflows/ci.yml/badge.svg)](https://github.com/iamnikolie/llmspend/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/iamnikolie/llmspend.svg)](https://pkg.go.dev/github.com/iamnikolie/llmspend)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

What your OpenAI and Anthropic orgs and Gemini API projects used and spent,
from the shell. One binary for all three, read with admin keys (Gemini: a
gcloud login), read-only.

```bash
llmspend usage --since 7d --by model     # every usage kind per model, with an estimated cost
llmspend usage --kind embeddings --by key
llmspend cost --month                    # billed cost, month to date, per org
llmspend cost --month last --by model
llmspend budget --alert-at 80            # spend limit vs month to date; exit 3 when close
llmspend keys                            # which key belongs to which project and org
llmspend prices gpt-4o                   # local price table; 'prices refresh' updates it
llmspend claude-code --since 30d         # Claude Code analytics per user
llmspend usage -p gemini --by project,model   # Gemini tokens per project, estimated cost
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

| | OpenAI | Anthropic | Gemini API |
| --- | --- | --- | --- |
| Usage | [Usage API](https://platform.openai.com/docs/api-reference/usage), all 11 families | [Usage report](https://platform.claude.com/docs/en/manage-claude/usage-cost-api) (Messages) | Cloud Monitoring token and request counts per project and model |
| Billed cost | Costs API | Cost report | only via the [Cloud Billing export to BigQuery](https://cloud.google.com/billing/docs/how-to/export-data-bigquery) |
| Spend limit / alerts | org and project spend limits and spend alerts | Enterprise only, not for Console orgs | not in an API |
| Keys → projects | projects, project keys, admin keys | API keys, workspaces | Google Cloud projects (no per-key usage) |
| Claude Code | — | [Claude Code Analytics API](https://platform.claude.com/docs/en/manage-claude/claude-code-analytics-api), Console-billed use only | — |
| Who can read it | any org, admin key (Read only is enough) | organization accounts only; admin key or a personal key with no workspace scope | a gcloud login with Monitoring Viewer on the projects |
| Prepaid credit balance | not in the API | not in the API | not in the API |

`llmspend` reads them, normalizes the shapes into one set of rows, and
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
  clicks **Create new admin key**, names it `llmspend`, sets
  **Permissions → Read only**, and copies the `sk-admin-…` secret. llmspend
  only reads, and a Read only key covers every endpoint it calls (checked
  live: all 11 usage families, costs, spend limits and alerts, projects,
  project and admin keys, /v1/me).
- **Anthropic** — two kinds of key work. Anthropic's docs say the Admin API
  is unavailable for individual accounts; a Console org named "…'s
  Individual Org" on the Team API plan did answer every endpoint in our live
  check, so try the key before setting up a new organization:
  - **Admin key**, if your Console still has
    [Settings → Admin keys](https://platform.claude.com/settings/admin-keys):
    **Create admin key**, name, expiration, copy `sk-ant-admin01-…`.
    Anthropic's docs say Console admin keys have no selectable scopes (full
    Admin API access), so there is no read-only option; the expiration is the
    lever.
  - **Personal key**, on Consoles with the newer *Keys & access → API keys*
    page ([platform.claude.com/settings/keys](https://platform.claude.com/settings/keys)):
    **Create key**, Linked account = yourself, **no workspace scope** (all
    workspaces), an expiration, copy `sk-ant-api03-…`. The Admin API accepts
    such a key with your own role's permissions. A key scoped to one
    workspace, or a legacy workspace key, is refused. **A personal key can
    also call the Messages API and spend money** — llmspend never does, but
    set an expiration and keep it in this config only.

```bash
pbpaste | llmspend config add personal-openai --provider openai --org-id org-…
pbpaste | llmspend config add team-anthropic --provider anthropic   # admin or personal key
llmspend orgs          # checks every key and shows the org it belongs to
llmspend config show   # keys masked
```

- **Gemini** — no key. An org is the Google Cloud projects behind your AI
  Studio API keys (`gen-lang-client-*`), read with your gcloud login:
  `--account you@gmail.com` (llmspend runs `gcloud auth print-access-token
  --account=…`, so the active gcloud account stays as it is), or Application
  Default Credentials without it. `--projects a,b` pins the projects;
  otherwise every `gen-lang-client-*` project the login can see is read.

```bash
llmspend config add gemini --provider gemini --account you@gmail.com
```

Keys live in `~/.llmspend/<profile>/config.yaml` (mode 0600, directory 0700).
`--config work` (or `LLMSPEND_CONFIG=work`) switches to another profile, so
personal and work orgs never mix. `OPENAI_ADMIN_KEY` and
`ANTHROPIC_ADMIN_KEY` add a temporary org without touching the file.

## Commands

| Command | What it shows |
| --- | --- |
| `usage [--since 7d] [--by model,key,project,day] [--kind K] [--bucket 1d\|1h]` | Every usage kind with a `kind` column: tokens (input / cached / cache-write / output, OpenAI and Gemini request counts, `est_usd`) or a quantity and unit (images, characters, seconds, calls, sessions, bytes, requests) |
| `cost [--month current\|last\|YYYY-MM] [--since 7d] [--by day,project,model,line,key]` | Billed USD; month to date by default, total under the table. Gemini: from the BigQuery billing export, with the billing currency next to USD |
| `budget [--projects] [--alert-at N]` | Month-to-date cost next to spend limits and alerts; exit 3 when a limit is N% used |
| `keys [--all]` | Every key the admin API lists, with project/workspace, org id, redacted hint, owner, last use |
| `projects [--all]` | OpenAI projects, Anthropic workspaces, Gemini Google Cloud projects |
| `orgs` | Each admin key (Gemini: gcloud login) checked against its provider, with the org id (and Anthropic org name; Gemini project ids) |
| `claude-code [--since 7d] [--by user,model,day,terminal]` | Sessions, commits, PRs, lines, tokens and Anthropic's cost estimate |
| `prices [model]` / `prices refresh` | The local price table; refresh re-reads the three public pricing pages |
| `skill` | The agent reference |

Every command runs across all orgs in the profile; `-p openai|anthropic|gemini` and
`--org a,b` narrow it. A failing org prints `! <org> (<provider>): …` on stderr
and the others still answer.

`--json` prints normalized rows; `--format csv|tsv|table`, `--fields a,b`.
Times are UTC; `--since` takes `7d`, `24h`, `2w`, `YYYY-MM-DD` or RFC 3339.

### Usage kinds

OpenAI reports usage per family; `usage` reads all of them and tags each row:
`completions` (Chat Completions and Responses), `embeddings`, `moderations`,
`decisions`, `images`, `speech`, `transcription`, `web_search`,
`file_search`, `code_interpreter`, `vector_stores`. Anthropic's report is one
kind, `messages`. Gemini has `generate` (text, image, speech and music
generation), `embeddings` and `predict` (Imagen and Veo, as request counts).
`--kind embeddings,completions` narrows the read. Token kinds are priced from
the local table; the others show a quantity and unit and leave the money to
`cost`.

### Gemini

Google publishes no API for billed Gemini cost or the AI Studio prepay
balance. `usage -p gemini` reads Cloud Monitoring instead: per project and
model, output tokens split by modality, input tokens and requests from the
quota metrics of every tier, embedding tokens, Imagen/Veo requests. `est_usd`
prices them from the table (image, audio and video output at their own
rates, Lyria per song, free tier at $0) and is an **estimate**: all input at
the uncached rate, standard tier, no grounding or cache-storage fees. Imagen,
Veo and per-image-only models are listed as not priced.

With a gcloud user login, Google bills the Monitoring calls to gcloud's own
client project, whose per-user per-minute quota several runs in a row can
use up (HTTP 429). llmspend backs off and retries; `--quota-project <id>`
bills a project of your own instead (it needs the Cloud Monitoring API
enabled).

The exact figure needs the Cloud Billing export to BigQuery. Once
`billing_table` is set, `cost` and `budget` read the export table with GET
calls only (schema, then `tabledata.list` per daily partition; no SQL job,
which would be a POST). To turn it on, as a Billing Account Administrator:

1. In the project that will hold the data, enable the BigQuery API and the
   BigQuery Data Transfer API, and create a dataset:
   `bq --project_id=<project> mk --location=EU --dataset billing_export`.
2. Console → **Billing** → the billing account → **Billing export** →
   **BigQuery export** → **Standard usage cost** → **Edit settings** → pick
   the project and dataset → **Save**. The table
   `gcp_billing_export_v1_<billing account id with _>` fills within hours;
   Google's docs say it backfills from the start of the previous month.
3. Point llmspend at it (repeat the other flags, `config add` replaces the
   entry):

```bash
llmspend config add gemini --provider gemini --account you@gmail.com \
  --billing-table <project>.billing_export.gcp_billing_export_v1_XXXXXX_XXXXXX_XXXXXX
llmspend cost -p gemini --by project
```

### Estimates vs bills

`usage` prices tokens with the local table at standard rates, so it can split
by key and by hour, which the cost reports cannot. `cost` is what the provider
bills. Batch and priority discounts, long-context rates and tool fees make the
two differ; trust `cost` for money and `usage` for where it went.

The price table ships inside the binary and is replaced by
`~/.llmspend/prices.json` after `llmspend prices refresh`, which parses the
providers' public pricing pages (Markdown). A refresh that finds no prices
leaves the table as it was. Gemini prices come from
[ai.google.dev/gemini-api/docs/pricing](https://ai.google.dev/gemini-api/docs/pricing)
(the rate in force on the refresh day; retired ids the API routes to a newer
model, per the deprecations page, are priced as that model).

### Limits

What the provider APIs do not give, stated plainly:

- **No prepaid credit balance** on any provider. `budget` shows the
  billing page link instead; `--alert-at` against an OpenAI spend limit is the
  closest warning the APIs allow.
- **Anthropic's Admin API is documented as organization accounts only.** If
  the API refuses an account as individual, llmspend prints one line with the
  fix: set up an organization at
  [platform.claude.com/settings/organization](https://platform.claude.com/settings/organization),
  then create a key. (A Console "Individual Org" on the Team API plan was
  accepted in our live check.)
- **Anthropic spend limits are Enterprise-only.** Console orgs get cost, not
  limits.
- **An empty table is a real empty.** Both providers return every day bucket
  in the range, empty or not; a missing parameter or header would be an error,
  not "No results". Widen `--since` (e.g. `--since 2026-05-01`) to see older use.
- **Claude Code analytics covers Console-billed use only** (API keys,
  Team/Enterprise seats), never Pro/Max subscriptions.
- **OpenAI spend limits and spend alerts are readable**, for the org and per
  project; `budget` compares month-to-date cost against them.
- Anthropic's cost report has no per-key split and leaves Priority Tier out.
- **Gemini has no cost API.** Without the BigQuery billing export its money
  is the `usage` estimate; usage has no per-key split.
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
