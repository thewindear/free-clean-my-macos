# fcm — free-clean-my-macos

A terminal junk scanner and cleaner for macOS that only ever lists what a
whitelist of **Scan Rules** declared safe to delete, shows the absolute path and
size of every item, and reports honestly how much space it actually freed.

- **Whitelisted, not guessed.** Nothing outside the rule set can appear in the
  list. No Full Disk Access, no `sudo`.
- **Honest arithmetic.** Cheap-regenerable items are deleted outright and count
  toward **Space Freed**; everything else is moved into the Trash and counts
  toward **Space Moved**, which frees nothing until you empty the Trash.
- **Auditable.** Every Cleanup writes one record per item to a JSONL Cleanup
  Log, and `fcm scan --json` gives you the same document the TUI renders.

## Install

```sh
brew tap thewindear/free-clean-my-macos https://github.com/thewindear/free-clean-my-macos
brew install free-clean-my-macos
```

Tarballs and `go install` are covered in [docs/install.md](docs/install.md).

## Use

```sh
fcm                     # the list screen: fold, select, filter, clean
fcm scan --json         # scriptable snapshot of every Junk Item
fcm clean --dry-run     # the plan a Cleanup would carry out
fcm clean --yes         # carry it out, with the Cleanup Log
fcm rules list          # the effective Scan Rules, overrides marked
```

Exit codes: `0` success, `1` partial failure, `2` usage error, `3` nothing to
clean.

Your own rules live in `~/.config/free-clean-my-macos/rules.d/*.yaml`; a rule
with the same name as an embedded one replaces it. The Cleanup Log lives in
`~/.local/state/free-clean-my-macos/cleanup-YYYYMM.jsonl`.

## Design

The domain vocabulary is in [GLOSSARY.md](GLOSSARY.md), and the decisions worth
arguing about are in [docs/adr](docs/adr): whitelist rules without Full Disk
Access, Go + Bubble Tea with the engine behind its own interface, and hybrid
deletion with the Trash out of the scan scope.
