# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go module for the LX-D02 thermal Bluetooth printer. Core library code lives at the repository root (`device.go`, `raster.go`, `lx-d02.go`) with focused packages in `bitmap/`, `fontmgr/`, and `ippsrv/`. The primary CLI is under `cmd/tp/`, with subcommand implementations in `cmd/tp/internal/`. `cmd/mkimage/` is a separate helper module for generating sample images. Tests are colocated with implementation files as `*_test.go`. Documentation and drawings live in `doc/`; sample input is in `samples/`.

## Build, Test, and Development Commands

- `go test ./...`: runs all tests in the main module. Some tests expect image fixtures in `media/` such as `media/rasterised.png` and `media/harold.jpg`.
- `go build ./...`: builds all packages in the main module, including the `cmd/tp` CLI.
- `go run ./cmd/tp help`: runs the CLI locally and prints available commands.
- `go run ./cmd/tp image -i image.png`: exercises image printing flow; use real printer access only when Bluetooth is configured.
- `cd cmd/mkimage && go run .`: runs the nested helper module to generate image assets.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed `.go` files before committing. Keep package names short and lowercase (`bitmap`, `ippsrv`, `fontmgr`). Exported identifiers should be documented when they are part of the library API. Prefer small files organized by behavior, as in `bitmap/dither.go` and `bitmap/scaling.go`. Tests should use descriptive names such as `TestRaster_Rasterise` or table-driven subtests.

## Testing Guidelines

Use Go’s built-in `testing` package; `testify` is available for assertions. Add tests beside the code they cover using the `*_test.go` pattern. For image-processing changes, include deterministic fixture-based assertions and document any required files under `media/`. Before opening a PR, run `go test ./...` from the repository root and, if relevant, test CLI behavior with `go run ./cmd/tp help`.

## Commit & Pull Request Guidelines

Recent commits use short, imperative, lowercase summaries such as `extract filter` and `fix the outdated test`. Keep commit subjects concise and focused on one change. Pull requests should describe the behavioral change, list test results, mention required fixtures or hardware assumptions, and link related issues. Include screenshots or sample output when changing generated images, printer output, or user-facing CLI help.

## Security & Configuration Tips

Do not commit local Bluetooth credentials, printer-specific secrets, generated traces, or large private image samples. Keep hardware-dependent behavior behind explicit commands and document platform assumptions when adding Bluetooth or IPP server changes.
