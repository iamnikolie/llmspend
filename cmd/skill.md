# llmspend — OpenAI + Anthropic usage and cost (agent reference)

Read-only view of what OpenAI and Anthropic orgs used and spent: tokens, billed
cost, spend limits, which key belongs to which project and org, a local price
table, Claude Code analytics. One binary for both providers. Markdown on
stdout, notes and per-org errors on stderr, `--json` for machines.

It only sends GET requests to the providers' admin APIs (the HTTP client has no
other method). No generation, nothing created or changed on the provider side.
Calls are free; the admin APIs do not bill.

## Setup
One admin key per provider org, stored in a profile:
`~/.llmspend/<profile>/config.yaml` (mode 0600; `--config <name>` or env
`LLMSPEND_CONFIG`, default `default`).

- OpenAI: org Owner opens https://platform.openai.com/settings/organization/admin-keys
  → Create new admin key → name `llmspend`, **Permissions → Read only** → copy
  `sk-admin-…`. Read only is enough for every llmspend command. Project keys
  (`sk-proj-…`) do not work.
- Anthropic: org admin opens https://platform.claude.com/settings/admin-keys →
  Create admin key → name, expiration → copy `sk-ant-admin01-…`. Per Anthropic's
  docs, Console admin keys have no selectable scopes (full Admin API access), so
  there is no read-only choice; set an expiration. Individual (non-organization)
  accounts have no Admin API.

```
pbpaste | llmspend config add personal-openai --provider openai --org-id org-…
pbpaste | llmspend config add team-anthropic --provider anthropic
llmspend orgs                      # checks every key, shows the org it belongs to
```
`config show` masks keys; `config rm <name>`, `config path`. `OPENAI_ADMIN_KEY` /
`ANTHROPIC_ADMIN_KEY` add a temporary `openai-env` / `anthropic-env` org.
`--org-id` is optional; `orgs` warns when the key's real org differs.

## Global
- `-p openai|anthropic` one provider; `--org a,b` only these config names.
  Default: every org in the profile, rows tagged `provider` and `org`.
- `--json` (= `--format json`) normalized rows; `--format md|table|csv|tsv`;
  `--fields a,b` picks columns.
- `--timeout 2m`, `--verbose` logs method/URL/status (never keys).
- One org failing (bad key, unsupported split) prints `! <org> (<provider>): …`
  on stderr and the others still print; exit 1 only when all failed.

## Commands
- `llmspend usage [--since 7d] [--until D] [--by model|key|project|day] [--kind K] [--bucket 1d|1h]`
  — usage of every kind. OpenAI: all Usage API families — `completions`
  (Chat Completions, Responses), `embeddings`, `moderations`, `decisions`,
  `images`, `speech`, `transcription`, `web_search`, `file_search`,
  `code_interpreter`, `vector_stores`. Anthropic: `messages`. Every row has a
  `kind`; `--kind embeddings,completions` narrows (default: all). `--by`
  combines (`--by project,model`); default `model`. Token kinds show requests
  (OpenAI), input (all input incl. cached and cache writes), cached,
  cache_write, output, est_usd. Other kinds show `quantity` + `unit` (images,
  characters, seconds, calls, sessions, bytes — vector-store bytes are the peak,
  not a sum) and no estimate; their money is in `cost`. `est_usd` = local
  price table × tokens at standard rates (batch/priority discounts ignored);
  stderr names token models with no price. Key and project ids resolve to names
  (Anthropic default workspace = `Default`, key-less console use =
  `(console / no key)`, kinds the API cannot split by key =
  `(not split by key)`). A family the org cannot read is a stderr note; the
  others still print.
- `llmspend cost [--month current|last|YYYY-MM] [--since 7d --until D] [--by day|project|model|line|key]`
  — billed USD from the cost reports. Default: this month to date, one row per
  org, total under the table. `--by model` reads OpenAI line items
  (`gpt-4o, input` → `gpt-4o`) and Anthropic descriptions; non-token Anthropic
  costs show as `(web_search)` / `(code_execution)`. `--by line` keeps the full
  line item. Anthropic has no per-key cost (→ use `usage --by key`) and leaves
  Priority Tier out of the cost report.
- `llmspend budget [--projects] [--alert-at 80]` (aliases `balance`, `limits`) —
  month-to-date cost vs the hard spend limit and spend alerts. OpenAI: org
  spend limit and spend alerts; `--projects` adds per-project spend limits.
  Anthropic Console orgs: cost only. No prepaid credit balance on either
  provider; the `note` column links the billing page. `--alert-at N`
  exits 3 with a stderr line when any limit is ≥ N% used (cron-friendly).
- `llmspend keys [--all]` — every key the admin API lists: provider, org, kind
  (project / service_account / admin / workspace), project or workspace, name,
  redacted hint, id, owner, status, created, last_used, org_id. Answers "which
  org and project is this key in". OpenAI: one call per project. Anthropic does
  not list Admin keys. `--all` adds archived/inactive/expired.
- `llmspend projects [--all]` (alias `workspaces`) — OpenAI projects, Anthropic
  workspaces.
- `llmspend orgs` (aliases `whoami`, `check`) — calls each org once, prints
  org_id (OpenAI from the `openai-organization` header, Anthropic from
  `/organizations/me` with org_name), masked key, status. Exit 1 if a key fails.
- `llmspend claude-code [--since 7d] [--by user|model|day|terminal]` (alias `cc`)
  — Anthropic Claude Code Analytics: sessions, commits, PRs, lines, tokens and
  Anthropic's estimated cost. One call per day. Covers Claude Code billed to the
  Console org only (API keys, Team/Enterprise seats), never Pro/Max
  subscriptions. Data lags ~1h.
- `llmspend prices [model-substring]` (alias `models`) — local price table, USD
  per 1M tokens: input, cached_input, cache_write (OpenAI 30m / Anthropic 5m),
  cache_write_1h (Anthropic), output, note. Built-in snapshot until refreshed.
- `llmspend prices refresh [-p provider]` — re-reads the public pricing pages
  (developers.openai.com and platform.claude.com, Markdown) into
  `~/.llmspend/prices.json`. Prints models/added/changed/removed per provider.
  Refuses to write when a page parses to zero prices.
- `llmspend skill`, `llmspend version`, `llmspend config add|rm|show|path`.

Times: `--since` takes `7d`, `24h`, `2w`, `YYYY-MM-DD` or RFC 3339; relative
values snap to UTC bucket starts (`7d` = today + 6 previous days). `--until` is
exclusive; a bare date includes that day. All dates are UTC.

## Workflows
- "What did this org spend this month?" → `llmspend cost --org <name>`.
- "Which model costs most?" → `llmspend cost --by model` (billed) or
  `llmspend usage --since 30d` (tokens + estimate).
- "Which key / app is burning money?" → `llmspend usage --since 7d --by key`
  then `llmspend keys` to see the key's project and org.
- "Which org is this key in?" → `llmspend keys --fields provider,org,org_id,project,name,hint`
  and match the hint.
- Daily spend chart → `llmspend cost --since 30d --by day --format csv`.
- Before the money runs out → `llmspend budget --alert-at 80` in cron; for the
  prepaid balance itself open the billing URL in the note.
- Stale estimates → `llmspend prices refresh`, then rerun `usage`.

## Limits (what the provider APIs do not give)
- **No prepaid credit balance** on either provider. `budget` links the billing
  page instead.
- **Anthropic Admin API is for organization accounts only.** An individual
  account gets a one-line error with the fix: set up an organization at
  platform.claude.com/settings/organization, then create an admin key.
- **Anthropic spend limits are Enterprise-only**; Console orgs get cost only.
- **Claude Code analytics covers Console-billed use only** (API keys,
  Team/Enterprise seats) — never Pro/Max subscriptions.
- **OpenAI spend limits and spend alerts are readable** (org and project) —
  that is what `budget` compares against.
- Anthropic's cost report has no per-key split and leaves Priority Tier out.

## Gotchas
- Usage and cost data lag ~5 minutes (Claude Code ~1 hour).
- `usage` est_usd is an estimate for token kinds; `cost` is what the provider
  bills, every kind included. They differ by discounts, batch, long-context
  and tool fees.
- An embeddings-heavy app shows up under `--kind embeddings`; the per-key
  split is `llmspend usage --kind embeddings --by key`.
- Admin keys are powerful. Create OpenAI ones as Read only; Anthropic Console
  admin keys cannot be limited (full Admin API access), so give them an
  expiration. llmspend only reads, but keep the key in this config only and
  revoke it in the console when done.
