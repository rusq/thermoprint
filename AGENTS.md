# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go module for the LX-D02 thermal Bluetooth printer. Core library code lives at the repository root (`device.go`, `raster.go`, `lx-d02.go`) with focused packages in `bitmap/`, `cupsraster/`, `fontmgr/`, and `ippsrv/`. The primary CLI is under `cmd/tp/`, with subcommand implementations in `cmd/tp/internal/`. `cmd/mkimage/` is a separate helper module for generating sample images. Tests are colocated with implementation files as `*_test.go`; package-specific fixtures live in `testdata/` directories. Documentation and drawings live in `doc/`; sample input and media are in `samples/`.

## Build, Test, and Development Commands

- `go test ./...`: runs all tests in the main module.
- `go build ./...`: builds all packages in the main module, including the `cmd/tp` CLI.
- `go run ./cmd/tp help`: runs the CLI locally and prints available commands.
- `go run ./cmd/tp image -i image.png`: exercises image printing flow; use real printer access only when Bluetooth is configured.
- `cd cmd/mkimage && go run .`: runs the nested helper module to generate image assets.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed `.go` files before committing. Keep package names short and lowercase (`bitmap`, `ippsrv`, `fontmgr`). Exported identifiers should be documented when they are part of the library API. Prefer small files organized by behavior, as in `bitmap/dither.go` and `bitmap/scaling.go`. Tests should use descriptive names such as `TestRaster_Rasterise` or table-driven subtests.

## Testing Guidelines

Use Go’s built-in `testing` package; `testify` is available for assertions. Add tests beside the code they cover using the `*_test.go` pattern.

- Name top-level tests after the unit under test and group related behaviors beneath them with descriptive `t.Run` subtests. For example, use `TestDashboardModel` with subtests for update, rendering, and boundary behavior rather than many unrelated top-level tests.
- Use table-driven tests when the same behavior has multiple inputs or expected outcomes. Give every case a clear, stable `name` and pass the case into the subtest safely.
- Keep each test focused on observable behavior. Avoid testing private implementation details, duplicating production logic in the test, or coupling tests to output formatting unless formatting is the behavior being verified.
- Make tests deterministic and isolated. Prefer `t.Helper`, `t.Cleanup`, `t.TempDir`, and `t.Setenv` for test setup and cleanup; do not rely on wall-clock timing, arbitrary sleeps, network services, Bluetooth hardware, or shared mutable global state.
- Use `t.Parallel()` only when the test and the code under test are safe to run concurrently. Do not parallelize tests that modify package globals, terminal/color configuration, process-wide logging, or shared fixtures.
- Assert useful failure context with `t.Fatalf`/`t.Errorf`, including the operation, actual value, and expected value. Use `errors.Is` or `errors.As` when checking wrapped errors.
- Add regression tests for bug fixes and edge cases, including empty input, limits, cancellation, and error paths where applicable. Keep fixtures small, deterministic, and documented; package-specific fixtures belong in the package's `testdata/` directory.
- Run `go test ./...` from the repository root before opening a PR. For concurrency-sensitive changes, also run `go test -race ./...`; for relevant CLI changes, run `go run ./cmd/tp help` and focused command tests.

## Go Implementation Guidelines

- Keep APIs small and idiomatic. Prefer simple concrete types until an interface is needed by a consumer, and avoid abstractions that do not improve the package’s behavior or testability.
- Propagate `context.Context` as the first parameter for operations that may block, honor cancellation, and do not store contexts in long-lived structs unless the design explicitly requires it.
- Wrap errors with operation context using `%w`; preserve sentinel and typed error checks with `errors.Is` and `errors.As`. Do not discard errors from cleanup or shutdown without a deliberate reason.
- Make ownership and concurrency explicit. Protect shared state, close resources in the component that owns them, and document lifecycle expectations for goroutines, channels, and servers.
- Avoid package-level mutable state. When it is required for CLI flags or process configuration, keep it narrowly scoped and reset or isolate it in tests.
- Prefer standard-library solutions, clear control flow, and small focused functions. Do not optimize prematurely; measure before introducing complexity.
- Keep changes narrowly scoped, update documentation for user-visible behavior, and run `gofmt` plus relevant tests for every changed Go file.

## Commit & Pull Request Guidelines

Recent commits use short, imperative, lowercase summaries such as `extract filter` and `fix the outdated test`. Keep commit subjects concise and focused on one change. Pull requests should describe the behavioral change, list test results, mention required fixtures or hardware assumptions, and link related issues. Include screenshots or sample output when changing generated images, printer output, or user-facing CLI help.

## Security & Configuration Tips

Do not commit local Bluetooth credentials, printer-specific secrets, generated traces, or large private image samples. Keep hardware-dependent behavior behind explicit commands and document platform assumptions when adding Bluetooth or IPP server changes.

## Solving issue from a BUGS.md file
If asked to work on an open issue from BUGS.md or similar file (a file that describes bugs or gaps), mark the issue as [DONE] when finished working.

For example, if there was a sample issue:
```
## [P3] Error-path encode failure is not logged; test assertions duplicated

Here goes the issue description ...

Proposed fix: ....
```
Once it is fixed, the Heading should be updated to 
```
## [DONE] [P3] Error-path encode failure is not logged; test assertions duplicated
<the rest remains untouched>
```
