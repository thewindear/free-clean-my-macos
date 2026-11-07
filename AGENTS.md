# AGENTS.md

Project: `free-clean-my-macos` — https://github.com/thewindear/free-clean-my-macos

## Agent skills

### Issue tracker

Issues and specs live in this repo's GitHub Issues (`thewindear/free-clean-my-macos`), operated with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` at the repo root plus `docs/adr/`. See `docs/agents/domain.md`.

## Before committing

`gofmt -l .` (must be empty), `go vet ./...`, `go test -count=1 ./...`. CI repeats all three on every push; the `-count=1` matters because the process-boundary tests exec a freshly built binary that Go's test cache cannot see.

## Reviewing a change

Read [`CODING_STANDARDS.md`](./CODING_STANDARDS.md) when you review. It carries the rule about a deferred review finding becoming an issue before the ticket closes.

## Landing a ticket

Tickets are worked on branches in separate `git worktree`s; `main` must be green after every landing.

```bash
./scripts/land-ticket.sh ticket/04-safety 4 --expect 'internal/engine/*:internal/owner/*'
```

The script merges with `--no-ff`, runs `go test -count=1 ./...`, pushes, and closes the issue. It **stops** on a conflict (aborting the merge) and **rolls `main` back** if the tests are red, so `main` is never left broken. Conflicts and red tests are judgement calls; the script refuses to guess.

Conflicts cluster in two places: the dispatch table in `internal/cli/cli.go` and the `Scan` loop in `internal/engine/engine.go`. Two lanes both touching one of them will conflict, so a ticket that rewrites either should say so in its body.
