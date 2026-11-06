# Scan only whitelisted rules, and never request Full Disk Access

The product promise is "every listed item is safe to delete". We get that only by inverting discovery: instead of walking the disk and judging what looks like junk, the scanner evaluates a finite set of declarative Scan Rules against known paths, and anything a rule does not claim never appears. Full Disk Access is not requested either, even though it would widen coverage, because the six scopes we do support (`~/Library/Caches`, logs, developer residue, `/private/var/folders`, Trash, orphans) are all readable without it — verified on macOS 27.0.1, where `~/Library/Caches`, `~/Library/Containers` and `/Library/Caches` read fine while `~/Library/Safari`, `Mail`, `Messages` and `MobileSync` are denied. Unreadable or unprotected-by-rule paths become `Skip`s with a reason.

## Considered Options

- **Full-disk walk with heuristics** ("large and untouched for N days"): unfalsifiable safety claim; a wrong deletion is unrecoverable trust.
- **Request Full Disk Access**: buys coverage over Mail/Safari/Messages backups, which we deliberately do not want to touch, at the cost of asking for the most invasive permission on macOS on first launch.
- **Whitelist rules, no FDA** (chosen): coverage is a maintainable list, reviewable in a diff, and every listing traces back to a specific rule.

## Consequences

- Coverage grows by editing rules, not by smarter guessing — new apps need new rules (users can add their own via `rules.d`).
- Any future feature that needs data outside the whitelist (e.g. browser cache inspection) is a scope change that reopens this ADR.

## Amendment (same session)

The Trash was in the original six scopes and is now out: `ls ~/.Trash` returns `Operation not permitted` without Full Disk Access, so the scope is unreachable by design. See ADR-0003.
