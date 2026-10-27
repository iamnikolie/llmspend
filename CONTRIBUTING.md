# Contributing

Thanks for taking a look. This is a small, focused CLI — bug reports and pull
requests are welcome, and so is a plain question in an issue.

## Reporting a bug

Include the output of `llmspend version`, the exact command you ran, and what
you expected instead. `--verbose` logs the HTTP requests (method, URL,
status) to stderr; keys are never printed.

**Never paste admin keys, org ids or usage data you would not publish into a
public issue.** Redact ids in pasted output.

## Pull requests

Before opening one:

```bash
make fmt           # gofmt -w .
make vet           # go vet ./...
make lint          # golangci-lint run ./...
make test          # go test ./...
```

CI runs the same (plus `-race`) on Linux and macOS, so a green local run
usually means a green PR.

House rules:

- **Read-only stays read-only.** The client in `internal/client` sends `GET`
  only. A feature that needs another method does not belong in this tool.
- **No network in tests.** `cmd/cmd_test.go` serves recorded responses from
  `cmd/testdata/`. New endpoints get a fixture there, written with made-up ids
  (`proj_test_…`, `example.com`) — never a real key, org id or email.
- **One concern per PR.** A bug fix and a refactor in the same diff take three
  times as long to review.
- **Keep the output token-lean.** The default columns exist so an agent can
  read them without burning context. Extra detail belongs behind `--json` or
  `--fields`, not in the default set.
- **Data goes to stdout.** Only errors, notes and `--verbose` logs go to
  stderr, so `x=$(llmspend ...)` always works.
- **Update the docs in the same commit.** Any change to the CLI surface must
  also update `cmd/skill.md` (embedded in the binary, printed by
  `llmspend skill`, and the single source of truth for command UX) and
  `README.md`.
- **Conventional commit subjects** — `feat:`, `fix:`, `docs:`, `refactor:`,
  `test:`, `chore:`. Release notes are generated from them.

## Releases

Maintainer-only. Tag and push:

```bash
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

GoReleaser builds archives for linux/darwin/windows on amd64 and arm64,
publishes the GitHub release with a generated changelog and, when the
`HOMEBREW_TAP_TOKEN` secret is set, updates the formula in
`iamnikolie/homebrew-tap`.
