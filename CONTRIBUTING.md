# Contributing to FreeTetra

FreeTetra is a young amateur-radio project. Contributions welcome — code,
docs, translations, bug reports, ideas.

## Bugs and feature requests

Open an issue on GitHub with:

- What you expected
- What actually happened
- Relevant logs (`journalctl -u freetetra` or stdout output)
- Your setup (server, BlueStation, hotspot/TMO-site, FreeTetra version /
  commit hash)

For security issues, please email do1xx@pm.me directly instead of opening
a public issue.

## Code contributions

1. Fork the repo
2. Make your changes on a feature branch
3. Make sure `go build ./...` and `go test ./...` succeed
4. Open a pull request with a short description of what and why

## Style

Standard Go style — `gofmt` is enforced. No special pre-commit hook needed,
just run `gofmt -w .` before committing.

For HTML/CSS in the page templates: keep the style consistent with the
existing pages (`internal/service/*_page.go`).

For new user-facing strings: add them to `internal/service/i18n_strings.go`
in both DE and EN, then use `{{T:key}}` placeholder in the template.

## Architecture overview

- `cmd/tetra-brew` — main server binary
- `cmd/tetra-brew-echo` — echo/parrot service bot
- `cmd/tetra-brew-webradio` — webradio service bot
- `cmd/tetra-brew-dmrbridge` — DMR/BrandMeister bridge
- `internal/brew/` — Brew protocol implementation (WebSocket, frames)
- `internal/federation/` — server-to-server peering (hub, mesh, peer)
- `internal/service/` — application logic (RadioID, APRS, coverage, web UI)
- `internal/config/` — env-var parsing
- `codec/` — ACELP codec (C, included)

## Federation message types

When adding a new federation feature, follow the existing pattern:

1. Add a new `Msg...` constant in `internal/federation/protocol.go`
2. Add the payload field(s) to the `Message` struct
3. Add `Broadcast...` method on `Hub` (`internal/federation/hub.go`)
4. Add handler in the message dispatch switch (around line 290 in hub.go)
5. Add `OnPeer...` to the `CallHandler` interface
6. Implement `OnPeer...` and `Notify...` wrapper in
   `internal/service/federation_bridge.go`
7. Call `Notify...` from the appropriate place in the service code

The mesh router (TTL + dedup + path) handles loop prevention automatically.

## Translations

To add a language beyond DE/EN:

1. Add a new map entry in `translations` in
   `internal/service/i18n_strings.go` (e.g. `LangFR: { ... }`)
2. Add the language code to `detectLang()` in `internal/service/i18n.go`
3. Add a `/lang/fr` handler registration

## License

By contributing you agree that your contribution is licensed under GPLv3.
