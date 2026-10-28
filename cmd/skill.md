# llmspend — OpenAI + Anthropic + Gemini usage and cost (agent reference)

Read-only view of what OpenAI and Anthropic orgs and Gemini API projects used
and spent: tokens, billed cost, spend limits, which key belongs to which
project and org, a local price table, Claude Code analytics. One binary for
all three. Markdown on stdout, notes and per-org errors on stderr, `--json`
for machines.

It only sends GET requests to the providers' admin APIs (the HTTP client has no
other method). No generation, nothing created or changed on the provider side.
Calls are free; the admin APIs do not bill (Google: Cloud Monitoring reads and
BigQuery `tabledata.list` are free).

## Setup
One admin key per provider org, stored in a profile:
`~/.llmspend/<profile>/config.yaml` (mode 0600; `--config <name>` or env
`LLMSPEND_CONFIG`, default `default`).

- OpenAI: org Owner opens https://platform.openai.com/settings/organization/admin-keys
  → Create new admin key → name `llmspend`, **Permissions → Read only** → copy
  `sk-admin-…`. Read only covers every endpoint llmspend calls (checked live).
  Project keys
  (`sk-proj-…`) do not work.
- Anthropic, either key works, both sent as `x-api-key`. Docs say individual
  accounts have no Admin API; a Console "Individual Org" on the Team API plan
  was accepted in the live check, so try the key first:
  - Admin key: https://platform.claude.com/settings/admin-keys → Create admin
    key → name, expiration → `sk-ant-admin01-…`. Per Anthropic's docs, Console
    admin keys have no selectable scopes (full Admin API access).
  - Personal key (Consoles with *Keys & access → API keys*, no Admin keys
    item): https://platform.claude.com/settings/keys → Create key → Linked
    account = yourself, no workspace scope, expiration → `sk-ant-api03-…`. Acts
    with your role's permissions. Workspace-scoped and legacy workspace keys are
    refused. It can also call the Messages API and spend money: set an
    expiration. `config add` accepts it and prints that note.
  - If the API ever answers that the key needs a workspace, re-add the org with
    `--workspace-id wrkspc_…` (sent as `anthropic-workspace-id`; off by default).
- Gemini: no key. One org = the Google Cloud projects behind your AI Studio
  API keys (`gen-lang-client-*`), read with a gcloud login: `--account <email>`
  runs `gcloud auth print-access-token --account=<email>` (never switches the
  active gcloud account); without it, Application Default Credentials
  (`gcloud auth application-default print-access-token`). `--projects a,b`
  pins project ids; default is every `gen-lang-client-*` project the login
  sees. The login needs Monitoring Viewer on each project (Owner has it).
  `--billing-table` adds the exact-cost source (see Gemini below).

```
pbpaste | llmspend config add personal-openai --provider openai --org-id org-…
pbpaste | llmspend config add team-anthropic --provider anthropic
llmspend config add gemini --provider gemini --account me@gmail.com
llmspend orgs                      # checks every key, shows the org it belongs to
```
`config show` masks keys (Gemini: the gcloud login); `config rm <name>`,
`config path`. `config add` with an existing name replaces the whole entry,
so repeat every flag. `OPENAI_ADMIN_KEY` /
`ANTHROPIC_ADMIN_KEY` add a temporary `openai-env` / `anthropic-env` org.
`--org-id` is optional; `orgs` warns when the key's real org differs.

## Global
- `-p openai|anthropic|gemini` one provider (`google` = gemini); `--org a,b` only these config names.
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
  `code_interpreter`, `vector_stores`. Anthropic: `messages`. Gemini:
  `generate` (generateContent: text, image, speech, music), `embeddings`,
  `predict` (Imagen, Veo: request counts only). Every row has a
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
  OpenAI image models (gpt-image-*) bill text and image tokens at different
  rates, so `usage` leaves them unpriced; `cost --by model` has their spend.
  Gemini rows come from Cloud Monitoring (see Gemini below); `--by key` shows
  `(not split by key)`, `--by project` names the Google Cloud project.
- `llmspend cost [--month current|last|YYYY-MM] [--since 7d --until D] [--by day|project|model|line|key]`
  — billed USD from the cost reports. Default: this month to date, one row per
  org, total under the table. `--by model` reads OpenAI line items
  (`gpt-4o, input` → `gpt-4o`) and Anthropic descriptions; non-token Anthropic
  costs show as `(web_search)` / `(code_execution)`. `--by line` keeps the full
  line item. Anthropic has no per-key cost (→ use `usage --by key`) and leaves
  Priority Tier out of the cost report. Gemini: only with a billing export
  table (else a per-org error pointing at `usage -p gemini`); `--by model` and
  `--by line` group by SKU; `amount` + `currency` columns appear for
  non-USD billing accounts, `usd` is converted with the export's own rate.
- `llmspend budget [--projects] [--alert-at 80]` (aliases `balance`, `limits`) —
  month-to-date cost vs the hard spend limit and spend alerts. OpenAI: org
  spend limit and spend alerts; `--projects` adds per-project spend limits.
  Anthropic Console orgs: cost only. Gemini: cost from the billing export
  when configured, else a note row (no limit or prepay-balance API). No
  prepaid credit balance on any provider; the `note` column says where. `--alert-at N`
  exits 3 with a stderr line when any limit is ≥ N% used (cron-friendly).
- `llmspend keys [--all]` — every key the admin API lists: provider, org, kind
  (project / service_account / admin / workspace), project or workspace, name,
  redacted hint, id, owner, status, created, last_used, org_id. Answers "which
  org and project is this key in". OpenAI: one call per project. Anthropic does
  not list Admin keys. `--all` adds archived/inactive/expired. Gemini orgs are
  skipped with a stderr note (usage has no per-key split).
- `llmspend projects [--all]` (alias `workspaces`) — OpenAI projects, Anthropic
  workspaces, Gemini Google Cloud projects (name from Resource Manager).
- `llmspend orgs` (aliases `whoami`, `check`) — calls each org once, prints
  org_id (OpenAI from the `openai-organization` header, Anthropic from
  `/organizations/me` with org_name), masked key, status. Exit 1 if a key fails.
  Gemini: gets a gcloud token and lists the projects (org_id = project ids).
- `llmspend claude-code [--since 7d] [--by user|model|day|terminal]` (alias `cc`)
  — Anthropic Claude Code Analytics: sessions, commits, PRs, lines, tokens and
  Anthropic's estimated cost. One call per day. Covers Claude Code billed to the
  Console org only (API keys, Team/Enterprise seats), never Pro/Max
  subscriptions. Data lags ~1h.
- `llmspend prices [model-substring]` (alias `models`) — local price table, USD
  per 1M tokens: input, cached_input, cache_write (OpenAI 30m / Anthropic 5m),
  cache_write_1h (Anthropic), output, note; Gemini rows add output_image,
  output_audio, output_video (per-modality output rates) and per_request (USD
  per song, Lyria). Built-in snapshot until refreshed; a provider missing from
  `prices.json` still comes from the snapshot.
- `llmspend prices refresh [-p provider]` — re-reads the public pricing pages
  (developers.openai.com, platform.claude.com, ai.google.dev, Markdown) into
  `~/.llmspend/prices.json`. Prints models/added/changed/removed per provider.
  Refuses to write when a page parses to zero prices. Gemini keeps the rate in
  force today ("$0.75 through December 31, 2026" → 0.75, note `until …`) and
  reads the deprecations page: a retired id the API routes to a newer model
  (`gemini-3.7-flash` → `gemini-3.8-flash`) is priced as that model.
- `llmspend skill`, `llmspend version`, `llmspend config add|rm|show|path`.

Times: `--since` takes `7d`, `24h`, `2w`, `YYYY-MM-DD` or RFC 3339; relative
values snap to UTC bucket starts (`7d` = today + 6 previous days). `--until` is
exclusive; a bare date includes that day. All dates are UTC.

## Gemini
Google has no API for billed Gemini cost or the AI Studio prepay balance.
llmspend has two sources:

1. **Estimate (always on): Cloud Monitoring.** Per project and model, per
   bucket: output tokens (`generate_content_usage_output_token_count`, split
   by output modality), input tokens and request counts (the quota-usage
   metrics of every tier: paid tier 1/2/3 and free), embedding tokens, and
   Imagen/Veo request counts. About 25 GET calls per project, 8 in parallel.
   `est_usd` = price table × tokens: text output at `output`, image/audio/video
   output at their own rates, Lyria per request (song), free-tier use at $0.
   **It is an estimate**: all input at the uncached rate (Monitoring does not
   split cached tokens), standard tier, no grounding or cache-storage fees.
   Not priced (stderr says so): Imagen and Veo (per image / per second of
   video; only request counts are published), models priced per image only,
   models not on the pricing page.
2. **Exact (once configured): Cloud Billing export to BigQuery.** `cost` and
   `budget` read the standard usage cost table with GET calls only (table
   schema, then `tabledata.list` per daily partition, from the range start to
   5 days after its end): rows whose service is the Gemini API and whose
   project is one of the org's, cost + credits, converted to USD with the
   row's `currency_conversion_rate`. No SQL is run (a query is a POST).

Enabling the export (once, in the Google Cloud console, by a Billing Account
Administrator):
1. Pick a project to hold the data (one of the `gen-lang-client-*` projects,
   or a new one) and enable the BigQuery API and BigQuery Data Transfer API on
   it.
2. Create a dataset, e.g. `bq --project_id=<project> mk --location=EU
   --dataset billing_export` (a multi-region location; EU or US).
3. Billing → pick the billing account → **Billing export** → **BigQuery
   export** → **Standard usage cost** → **Edit settings** → choose the project
   and dataset → **Save**. Google creates
   `gcp_billing_export_v1_<BILLING_ACCOUNT_ID with _>` and fills it within
   hours. Google's docs say the standard export backfills from the start of
   the previous month (check what landed); anything older stays
   estimate-only.
4. `llmspend config add <name> --provider gemini --account <email>
   --billing-table <project>.billing_export.gcp_billing_export_v1_XXXXXX_XXXXXX_XXXXXX`
   (repeat `--projects` if you pinned them), then `llmspend cost -p gemini`.
   The login needs BigQuery Data Viewer on the dataset (project Owner has it).

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
- "What does Gemini cost per app?" → `llmspend usage -p gemini --since 30d
  --by project,model` (estimate); with the billing export,
  `llmspend cost -p gemini --by project`.

## Limits (what the provider APIs do not give)
- **No prepaid credit balance** on any provider. `budget` links the billing
  page instead.
- **Gemini has no cost API.** Without the BigQuery billing export, Gemini
  money is the `usage` estimate only; Monitoring has no per-key split, and
  Imagen/Veo use shows as request counts without a price.
- **Anthropic Admin API is documented as organization accounts only** (a
  Console "Individual Org" on the Team API plan worked live). If the API
  refuses an account as individual, the error says: set up an organization at
  platform.claude.com/settings/organization, then create an admin key or an
  unscoped personal key. Every Anthropic refusal (401/403/404) prints one line
  with the reason (rejected key, workspace-scoped key, role without access,
  individual account) and the fix.
- **Anthropic spend limits are Enterprise-only**; Console orgs get cost only.
- **Claude Code analytics covers Console-billed use only** (API keys,
  Team/Enterprise seats) — never Pro/Max subscriptions. If Claude Code shows
  5-hour / 7-day plan limits, it runs on a claude.ai subscription and
  `claude-code` is empty by design; its spend is not in either API.
- **Empty means empty.** The reports return every day bucket in range; "No
  results" with exit 0 is real zero use, not a missing parameter (those are
  errors). Widen `--since` to find older use.
- **OpenAI spend limits and spend alerts are readable** (org and project) —
  that is what `budget` compares against.
- Anthropic's cost report has no per-key split and leaves Priority Tier out.

## Gotchas
- Usage and cost data lag ~5 minutes (Claude Code ~1 hour; the Gemini billing
  export several hours).
- Gemini prices change on dates the page states; the table keeps the rate in
  force at refresh time, so refresh after such a date.
- `usage` est_usd is an estimate for token kinds; `cost` is what the provider
  bills, every kind included. They differ by discounts, batch, long-context
  and tool fees.
- An embeddings-heavy app shows up under `--kind embeddings`; the per-key
  split is `llmspend usage --kind embeddings --by key`.
- Admin keys are powerful. Create OpenAI ones as Read only; Anthropic Console
  admin keys cannot be limited (full Admin API access) and personal keys can
  spend money on the Messages API, so give them an expiration. llmspend only reads, but keep the key in this config only and
  revoke it in the console when done.
