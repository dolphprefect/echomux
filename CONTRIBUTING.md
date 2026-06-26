# Contributing to echomux

echomux is open-source and lives on contributions. Thank you for considering one.

---

## This is a TDD project

Every change must be accompanied by tests. No untested code merges. This applies to:

- New features — write the test first, then the implementation
- Bug fixes — write a failing test that reproduces the bug, then fix it
- Refactors — ensure the existing tests still pass unchanged; add tests for any new surface area

### Tests cannot be changed to make them pass

If a change breaks a test, the implementation is wrong, not the test. Tests are the specification. The only valid reasons to modify a test are:

1. The test was wrong about the expected behaviour (this must be documented in the PR)
2. A deliberate behaviour change that has been discussed and agreed in an issue first

If you catch yourself changing a test to avoid a failure, stop and fix the code instead.

---

## What to know before you touch the code

### Server (Go)

- Handler tests are in `service/internal/api/handlers_test.go`, `autorouter_test.go`, and `scan_internal_test.go`
- The BT manager, audio controller, and PipeWire are all behind interfaces — use the mocks in tests, never spawn real processes
- `tickRouter` tests run the real goroutine with a real `exec.Command`; keep timing generous (1–2 s for process start, not tight polls)
- The `volume=0` case must always be distinguishable from "volume never set" — use two-value map lookup (`v, ok := s.volumes[mac]`), never check `if v > 0`
- JSON field casing matters: `bluetooth.Device` embedded struct marshals capital-case (`MAC`, `Name`, `Connected`, `Paired`); per-speaker fields (`muted`, `playing`, `volume`, `delay_ms`) have lowercase json tags

### Client (Svelte / JS)

- Tests are in `service/ui/src/` alongside the components they test, using Vitest 2.x + jsdom 24 + @testing-library/svelte 4
- `onMount` does **not** fire in the test environment — tests that require it are skipped with an explanatory comment. Do not remove the skips; do not try to make `onMount` work by patching the environment
- When polling `GET /devices`, always normalise lowercase API fields to the capital-case the components use: `{ ...d, Muted: d.muted, Playing: d.playing }`
- Backdrop click handlers on sheet components need `svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions` to suppress false-positive a11y warnings (keyboard navigation is not relevant for a touch-first mobile UI)

### Audio graph

- Volume=0 is a valid state and must be applied on loopback restart, not skipped
- Per-speaker delay changes kill and respawn the pw-loopback process — this is the only way to adjust delay at runtime; there is no hot-path
- The zombie watchdog has a 30 s cooldown; `POST /playback/restart` clears it so the watchdog acts immediately

---

## Multi-step verification checklist

Before submitting a PR, an AI agent or human contributor must go through every step:

1. **Tests pass** — `go test ./...` in `service/` and `npm test` in `service/ui/` both green
2. **No tests were weakened** — diff the test files and confirm no assertion was removed or made looser
3. **Mocks were not abused** — if a mock was extended, it was for testability, not to hide a real failure
4. **JSON contract is intact** — check that field names match between Go handlers and Svelte components; the API doc in `ECHOMUX-API.md` is the source of truth
5. **Edge cases are covered** — zero values (`volume=0`, `delay=0`), empty lists, and error paths all have explicit tests
6. **Documentation is updated** — if an API endpoint changed, update `ECHOMUX-API.md`; if the architecture changed, update `ECHOMUX-SYSTEM.md`
7. **No commented-out code** — remove it or don't commit it

---

## Pull request expectations

- One PR per logical change — don't bundle unrelated fixes
- PR description explains *why*, not just *what*. The diff shows what; the description tells the reader why this was the right approach
- If the change touches the audio graph, BT connection lifecycle, or tickRouter timing: test on real hardware (a Pi with at least two BT speakers) before marking it ready for review
- Link to any BlueZ / PipeWire / WirePlumber upstream bugs that informed the approach

---

## Build and deploy

The repo contains a `Makefile` with core targets and a `Makefile.local` (gitignored) for machine-specific overrides such as the satellite SSH host. The targets below assume `Makefile.local` is present and configured.

```bash
make deps            # npm install for the UI (run once, or after package.json changes)
make build           # build UI then compile Go binary → service/echomux
make deploy-master   # build + stop service + copy binary + start service (on the master Pi)
make deploy-satellite # build + scp binary to satellite + restart service there
make deploy-all      # build + deploy-master + deploy-satellite, prints service status for both
```

`Makefile.local` must define `SATELLITE_HOST`, `SATELLITE_KEY`, and the SSH/SCP aliases. See the existing `Makefile.local` for the expected variable names.

---

## Project structure

```
service/
  cmd/echomux/        main.go — flag parsing, server wiring
  internal/
    api/              HTTP handlers, WebSocket events, satellite proxy, node registry
    audio/            PipeWire / pactl controller and loopback management
    bluetooth/        BlueZ DBus manager
  ui/src/             Svelte UI (components, lib, tests)
  echomux.service     systemd unit template
  setup/              install.sh interactive setup script
```

---

## Running the test suites

```bash
# Go (from repo root)
cd service/
go test ./...

# JavaScript (from repo root)
cd service/ui/
npm test

# Both, in one shot
cd service/ && go test ./... && cd ui/ && npm test
```

For verbose Go test output:

```bash
cd service/
go test -v -count=1 ./...
```

---

## Questions and discussions

Open an issue. Don't ask in PRs — conversation on a PR is for the PR's specific code, not design questions.

---

## Supporting the project

echomux is free software. If it brings music to your home, consider buying the maintainer a coffee:

[☕ buymeacoffee.com/dolphprefect](https://buymeacoffee.com/dolphprefect)
