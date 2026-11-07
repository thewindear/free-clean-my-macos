# Coding standards

Read this when you **review** a change, not when you write one. The implementation agent has the context pressure; the reviewer has the room to hold rules.

## What counts as a violation

- **Mechanical** (a banned API, an import shape, a file-location rule, a formatting pattern): it belongs in a check, not here. `gofmt`, `go vet` and the test suite run in CI; if a mistake is mechanical and no check catches it, adding the check is the fix.
- **Judgement** (cross-file consistency, "does this match the surrounding design", naming that drifted from the domain model): that is what this file is for.

## Rules

- **Language**: Go 1.24, standard library first; no cgo. See `docs/adr/0002-*`.
- **Vocabulary**: use the terms defined in `GLOSSARY.md` verbatim — `Junk Item`, `Owner`, `Scan Rule`, `Risk Level`, `Rebuild Cost`, `Recommended`, `Rule Precedence`, `Space Freed`, `Space Moved`. Synonyms in identifiers, UI strings, test names or docs are a violation, and they are how the domain model rots.
- **Safety invariants** (from `docs/adr/0001-*` and `0003-*`): never request Full Disk Access, never call sudo, never follow symlinks while sizing, and never let a test write inside the real `~/Library` or the real Trash. A change that weakens one of these needs a superseding ADR, not a code review comment.
- **Tests live at two seams**: the process boundary (build `fcm`, run it with `HOME` / `FCM_APPS_DIRS` / `FCM_SYSTEM_ROOT` pointed at a fixture) and pure functions (rule precedence, recommendation policy, formatting). A test against an internal helper is a violation even when it passes.
- **Honest numbers**: anything user-facing that reports space, counts or progress must come from the engine once. A UI that recomputes a size, or a Summary that adds freed and moved bytes into one number, is a violation — `docs/adr/0003-*` exists because of it.
- **`-count=1` when running tests by hand.** The process-boundary tests exec a freshly built binary, which Go's test cache cannot see; a cached pass is a false green.

## A deferred review finding becomes a ticket before the ticket closes

If a review records a gap it decided not to fix (an unimplemented flag, an untested path, a deviation from the spec), open an issue for it in the same breath, with acceptance criteria, and link it from the closing comment. Findings that live only in a review summary get rediscovered: `--permanent` was flagged by two consecutive reviews before it became `#9`, and that cost two extra rounds.

## Hot files

`internal/cli/cli.go` and `internal/engine/engine.go` are where parallel work collides — the usage/dispatch table and the `Scan` loop respectively. A ticket that rewrites either of them should say so in its body, so the second lane knows to expect a merge conflict instead of discovering it.
