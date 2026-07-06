# [DONE] Variable-Length Roll Media

## Summary

Support 58 mm roll paper by advertising a fixed 48 mm printable width with variable page height from 20 mm to 1000 mm. Keep the existing fixed label sizes for compatibility, add explicit custom-size capability for driverless IPP/AirPrint and PPD clients, and trim trailing blank rows only for custom roll-height jobs so oversized custom pages do not feed excessive blank paper.

## Key Changes

- Add roll-size constants shared by the IPP media helpers:
  - Printable width: `4800` hundredths of mm, matching 384 px at 203 dpi.
  - Minimum custom height: `2000` hundredths of mm.
  - Maximum custom height: `100000` hundredths of mm.
- Keep `media-default` as the existing fixed `om_label-48x100mm_48x100mm`.
- Keep existing fixed `om_label-48x...` entries in `media-supported`.
- Add PWG custom-size keywords to `media-supported`, using custom min/max forms equivalent to 48 x 20 mm and 48 x 1000 mm, without feeding those names into fixed-size default parsing.
- Extend media collection generation so fixed media still comes from parseable self-describing names, while the custom range entry is constructed explicitly.
- Add range-aware `media-size-supported` / `media-col-database` entries:
  - `x-dimension` fixed to 4800.
  - `y-dimension` as a range from 2000 to 100000.
  - Margins remain zero for the 48 mm printable coordinate system.
- Add `media-col-supported` with the member keywords the server accepts for media collections: `media-size`, `media-top-margin`, `media-bottom-margin`, `media-left-margin`, and `media-right-margin`.
- Consume job-submitted `media` / `media-col` only to decide whether blank-row trimming is allowed. Do not add full job-side media validation: `Validate-Job` still accepts requests without rejecting unsupported media, and clients remain responsible for rasterising to their chosen page size.

## Interface and Data Flow

- Keep the public `thermoprint.LXD02.PrintImage` and `ippsrv.Driver` interfaces unchanged.
- Add an unexported `ippsrv` print option, for example `printJobOptions{trimTrailingBlank: bool}`, and an unexported `basePrinter.print(ctx, data, opts)` helper.
- Keep the public `Printer.Print(ctx, data)` method as a compatibility wrapper that calls the helper with trimming disabled.
- In the IPP Print-Job / document submission path, inspect operation/job attributes before calling the printer helper:
  - Set `trimTrailingBlank=true` when `media` is a submitted custom self-describing name such as `custom_48x150mm_4800x15000`, with width `4800` and height inside the custom range.
  - Set `trimTrailingBlank=true` when `media-col.media-size` has `x-dimension=4800` and a `y-dimension` inside the custom range that does not equal an advertised fixed-label height.
  - Set `trimTrailingBlank=false` for fixed `om_label-48x...` media, missing media attributes, unparsable media attributes, and direct public `Printer.Print` calls.
- Do not infer roll/custom mode from decoded raster dimensions alone; that would risk trimming fixed-label jobs from clients that omit media attributes.
- When trimming is enabled, apply it in `ippsrv` after image decode/filtering and page composition, immediately before calling `Drv.PrintImage`.

## Blank Feed Handling

- Use the IPP job media decision from the print path; only jobs explicitly submitted with custom variable-height media get trailing blank-row trimming.
- Do not trim existing fixed label media by default. Fixed labels must continue advancing the full selected label height so die-cut stock stays aligned.
- For direct CLI image printing, keep current behavior unless a separate explicit trim/roll option is added later.
- For custom roll jobs, trim trailing all-white rows in `ippsrv` before calling `Drv.PrintImage`; `PrintImage` then performs the existing resize/dither/serialise flow unchanged.
- Preserve at least one row and do not trim leading whitespace or internal whitespace.
- Document this as roll-paper behavior: page height is an upper bound selected by the client, while printed/feed length follows actual visible content plus any non-white bottom content supplied by the job.

## Bonjour and PPD

- Update `ippsrv/bonjour.go` TXT metadata so `PaperMax` no longer understates the roll length; use a bucket larger than `isoC-A2`, because 1000 mm exceeds A2/C2 long-edge sizes.
- Review `kind` and URF hints, but keep `kind=label` and grayscale URF unless testing shows clients require a paper/roll classification change.
- Update `ippsrv/ppd/LX-D02.ppd` so both fixed sizes and custom sizes use the same 48 mm printable-width page model as IPP. Convert the existing fixed `PageSize`, `ImageableArea`, `PaperDimension`, and `PageRegion` widths from the current 58 mm value to 48 mm equivalents to avoid fixed/custom scaling differences within the same PPD.
- Add the full PPD custom-size fields required by `cupstestppd`:
  - `*VariablePaperSize: True`
  - `*MaxMediaWidth`, `*MaxMediaHeight`
  - `*HWMargins`
  - `*CustomPageSize`
  - five `*ParamCustomPageSize` entries: `Width`, `Height`, `WidthOffset`, `HeightOffset`, `Orientation`
  - compatible `*NonUIConstraints` entries if required by the custom page parameters.

## Test Plan

- Add or update IPP attribute tests to assert:
  - Existing fixed media names remain present.
  - Custom min/max media keywords are present in `media-supported`.
  - `media-size-supported`, `media-col-database`, `media-col-supported`, and `media-col-default` encode and decode through `goipp`.
  - Default media remains `om_label-48x100mm_48x100mm`.
- Add media helper tests for fixed dimensions, custom keyword filtering, and explicit custom range collection construction.
- Add print-path tests for media-based trim gating: custom keyword trims, custom `media-col` trims, fixed media does not trim, missing/unparsable media does not trim, all-white input preserves at least one row, content at the bottom edge is preserved, and already-tight content is unchanged.
- Run `go test ./...`.
- Run `make -C ippsrv/ppd test`.
- Manually verify generated AirPrint/PPD behavior with a custom-height page if available, especially that short content on a long selected page does not feed the full selected height.

## Assumptions

- Maximum advertised roll length is 1000 mm.
- Minimum custom page height is 20 mm.
- The driver advertises and renders in the 48 mm printable coordinate system, not the full 58 mm stock width.
- Existing fixed sizes remain for clients that do not expose custom paper sizes well.
- Job-side media validation is out of scope beyond detecting explicit custom-size media for trimming; unsupported client media requests are not rejected unless they fail during raster decoding or printing.
