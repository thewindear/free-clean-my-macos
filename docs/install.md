# Installing fcm

Three ways to get a single `fcm` binary on a Mac. None of them needs `sudo`,
Full Disk Access, an Apple Developer ID, notarization, or a runtime to install:
the binary is built with `CGO_ENABLED=0` and is the only thing that ships.

## Homebrew

This repository doubles as its own tap — the release workflow writes
`Formula/free-clean-my-macos.rb` for every tagged release, with that release's
checksums:

```sh
brew tap thewindear/free-clean-my-macos https://github.com/thewindear/free-clean-my-macos
brew install free-clean-my-macos
fcm
```

`brew install` runs the formula's test, which asks the installed binary for its
version, so a broken tarball fails the install instead of surprising you later.

## Release tarball

Pick the tarball for your architecture (`arm64` for Apple silicon, `amd64` for
Intel) from the [releases page](https://github.com/thewindear/free-clean-my-macos/releases),
then verify and unpack it:

```sh
shasum -a 256 -c checksums.txt          # in the same directory as the tarball
mkdir -p ~/.local/bin
tar -xzf fcm_<version>_darwin_<arch>.tar.gz -C ~/.local/bin fcm
~/.local/bin/fcm version
```

## From source

```sh
go install github.com/thewindear/free-clean-my-macos@latest
```

Go 1.24 or newer. This is what the release workflow does, with
`-trimpath -ldflags "-s -w -X .../internal/cli.version=<tag>"` and
`CGO_ENABLED=0` so the result is a static, self-contained binary.

## Verifying a release

```sh
fcm version          # fcm <tag>
shasum -a 256 fcm_<version>_darwin_<arch>.tar.gz   # matches checksums.txt
```

## What fcm never asks for

- No Full Disk Access. The scan only reads paths a Scan Rule claims, all of
  which are readable without it (ADR-0001).
- No `sudo`. Everything fcm touches lives under `$HOME` (or `/Library/Caches`,
  which it only reads).
- No signing or notarization step. Binaries installed by Homebrew or
  `go install` carry no quarantine attribute; Gatekeeper is not in the path.

## Upgrading and removing

```sh
brew upgrade free-clean-my-macos
brew uninstall free-clean-my-macos
```

Your own configuration is not touched: Scan Rules stay in
`~/.config/free-clean-my-macos/`, and the Cleanup Log stays in
`~/.local/state/free-clean-my-macos/`.
