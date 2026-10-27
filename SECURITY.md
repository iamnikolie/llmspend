# Security Policy

## Supported versions

The latest release is the supported one. Fixes land on `main` and go out in the
next tag.

## Reporting a vulnerability

Please do **not** open a public issue for a security problem.

Use GitHub's private vulnerability reporting instead:
[Security → Report a vulnerability](https://github.com/iamnikolie/llmspend/security/advisories/new).
That opens a private advisory visible only to the maintainers.

Include what you did, what happened, and the impact you think it has. Expect a
first response within a week — this is a spare-time project, not a product with
an on-call rotation.

## Scope notes

Some things are known and by design rather than vulnerabilities:

- **Admin keys are stored in plain text** in `~/.llmspend/<profile>/config.yaml`
  at mode 0600 in a 0700 directory — the same posture as `~/.aws/credentials`.
  Anyone who can read your home directory can use them.
- **Admin keys can do more than llmspend does.** Create the OpenAI key with
  **Permissions → Read only**; llmspend only reads. An
  Anthropic Console admin key cannot be limited — per Anthropic's docs it
  carries full Admin API access (Console keys have no scopes) — so give it an
  expiration. llmspend only ever sends `GET` — its HTTP client has no other
  method — but the key itself may be powerful. Revoke it when you stop using
  llmspend.
- **What is printed.** `config show` and `orgs` mask keys to the prefix and the
  last four characters. `keys` prints the providers' own redacted hints, never a
  secret. `--verbose` logs method, URL and status only; headers are not logged.
- **Output contains account data**: org ids, project and key names, owner
  names and, for `claude-code`, user emails. Treat it like the console it comes
  from before pasting it anywhere public.
- **`prices refresh`** fetches two public pages (developers.openai.com,
  platform.claude.com) without credentials.
