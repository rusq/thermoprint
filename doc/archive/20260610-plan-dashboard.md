# TUI Dashboard For `tp server`

## Summary

Build a read-only Bubble Tea dashboard for `tp server`. It runs by default on
interactive terminals, replaces raw scrolling logs with a structured terminal
UI, and keeps `-log` file output working. Add `-no-tui` to preserve the current
plain log behavior.

The dashboard shows:

- IPP server state: listen address, uptime, mDNS enabled/disabled, debug/dumpdir
  status.
- Connection/printer state: connected/dry-run, IPP printer state, active print
  FSM state, battery, charging/charged, paper status.
- Jobs: pending/processing/completed/aborted jobs with ID, name, user, format,
  state, created/started/completed times.
- Logs: recent `slog`/standard log records in a scrollback panel.

## Key Changes

- Add Bubble Tea-based UI code under `cmd/tp/internal/cmdserver` or a small
  sibling internal package used only by the CLI.
- Add `tp server -no-tui`; TUI starts only when stdout/stderr is an interactive
  terminal and `-no-tui` is false. Non-interactive runs keep existing log output.
- Add an `ippsrv.Snapshot()` style read-only API on `Server` that returns stable
  copies of server/printer/job state without exposing mutable spool internals.
- Add a small observer/snapshot surface to `thermoprint.LXD02` for connection,
  print FSM state, and last decoded status. Treat `NoPaper` as the displayed
  "Paper out / lid open" status per product decision.
- Add a TUI-aware `slog.Handler` that appends bounded log entries to the
  dashboard while still writing to `-log` when configured.
- Keep v1 read-only: keys are limited to quit, help, log scroll, and job/log
  focus movement. No job cancel, retry, clear, or admin actions.

## Public Interfaces

- `ippsrv.Server.Snapshot() ippsrv.ServerSnapshot`
- Snapshot structs for server/printer/job data with plain value fields only.
- Optional printer capability interface, for example
  `type StatusInformer interface { Snapshot() thermoprint.PrinterSnapshot }`,
  used by the server command when wrapping `*LXD02`.
- New CLI flag: `tp server -no-tui`.

## Test Plan

- Unit-test snapshot APIs for copying job state safely and not exposing mutable
  `Job` pointers/slices.
- Unit-test status parsing/storage: battery, charging, charged, and no-paper/lid
  open display state.
- Unit-test TUI model update logic with synthetic snapshots and log records.
- Unit-test server command mode selection: interactive default TUI, `-no-tui`,
  and non-interactive fallback.
- Run `go test ./...`.
- Manually verify `go run ./cmd/tp server -dry` shows the dashboard and accepts a
  local IPP job without logs corrupting the UI.

## Assumptions

- Bubble Tea/lipgloss dependencies are acceptable.
- Paper out and lid open are represented by the existing `NoPaper` status bit and
  should be displayed as one combined paper/lid status.
- Dashboard v1 is observability only; control actions are out of scope.
- Existing `SIGINFO` output remains available and can use the same snapshot data
  later.
