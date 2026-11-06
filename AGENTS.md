# AGENTS.md

Project: `free-clean-my-macos` — https://github.com/thewindear/free-clean-my-macos

## Agent skills

### Issue tracker

Issues and specs live in this repo's GitHub Issues (`thewindear/free-clean-my-macos`), operated with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` at the repo root plus `docs/adr/`. See `docs/agents/domain.md`.

## Coding standards

- **Language**: Go 1.24, standard library first; no cgo. See `docs/adr/0002-*`.
- **Vocabulary**: use the terms defined in `GLOSSARY.md` verbatim — `Junk Item`, `Owner`, `Scan Rule`, `Risk Level`, `Rebuild Cost`, `Recommended`, `Rule Precedence`, `Space Freed`, `Space Moved`. Do not invent synonyms for them in identifiers, UI strings or tests.
- **Safety rules** (from `docs/adr/0001-*` and `0003-*`): never request Full Disk Access, never call sudo, never follow symlinks while sizing, and never let a test write inside the real `~/Library` or the real Trash.
- **Tests live at two seams**: the process boundary (build `fcm`, run it with `HOME` / `FCM_APPS_DIRS` pointed at a fixture) and pure functions (rule precedence, recommendation policy). No tests against internals.
- **Before committing**: `gofmt -l .` (must be empty), `go vet ./...`, `go test -count=1 ./...`. The `-count=1` matters: the process-boundary tests exec a freshly built binary, which Go's test cache cannot see.
