# Cleanup deletes low-cost items outright and trashes the rest

Moving everything to the Trash is the safe-sounding default and it fails the product's only real job. On this machine `mv` into `~/.Trash` is a same-volume rename (0.03 s for a 2 MB directory), so a 32 GB "cleanup" frees zero bytes, and `~/.Trash` cannot even be listed without Full Disk Access — we cannot show its contents, cannot size it, and cannot empty it for the user. A cleaner that reports success while free space stays flat is worse than one that deletes.

So the deletion mode is a function of the rule, not a global setting:

- `risk=safe` **and** `rebuild_cost ∈ {none, low}` → deleted outright. These are caches the app regenerates by itself.
- everything else (`caution`, `orphan`, `rebuild_cost=high`) → moved into the Trash.

Safety therefore rests on the Scan Rules — the thing this project actually invests in — instead of on the Trash as a universal undo. The Summary reports **Space Freed** and **Space Moved** as separate numbers so neither claim is inflated.

## Consequences

- The Trash stops being a scan scope: it is unreadable without Full Disk Access, which ADR-0001 refuses to request. The tool only *writes* to it.
- `rebuild_cost=high` items (JetBrains indexes, `ms-playwright` browsers, CocoaPods pods, iOS DeviceSupport) are safe but stay recoverable, because "safe" is not the same as "cheap to get back".
- Permanent deletion has no undo, so the confirm gate in Q20 (≥ 5 GB, any `caution`, `--permanent`) is the only guard on the irrecoverable path.
