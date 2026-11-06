# Go + Bubble Tea for the TUI, with the engine behind its own interface

The UI is a throwaway shell around a scanner/cleaner core, because a SwiftUI GUI is planned later and because the same core must be scriptable. Go carries the distribution story: one static binary per architecture, `GOOS=darwin GOARCH=arm64|amd64` cross-compile, `go install` and a Homebrew tap later, and no runtime for users to install. Bubble Tea supplies the list/progress/spinner pieces the Cleanup Progress and Scan Progress screens need, and goroutines suit a scanner whose bottleneck is I/O (a `du` of one 32 GB cache directory already takes 11 s).

## Consequences

- The engine must never import the TUI package, and must be usable from a `--json` CLI path with no terminal.
- A future SwiftUI shell talks to the engine over a subprocess/JSON boundary, not by linking Go code.
- A Go TUI installed via brew or `go install` carries no quarantine attribute, so Gatekeeper is not a distribution problem; notarization stays out of scope until a `.app` bundle exists.
