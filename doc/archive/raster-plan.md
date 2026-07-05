# Client-side rasterisation: PWG Raster + Apple URF support (branch `cups-raster`)

## Context

Today CUPS sends `application/pdf` to the IPP server, which shells out to the external `magick` binary (ImageMagick → Ghostscript) to rasterise it (`ippsrv/filter.go`, `imageMagickFilter`). By advertising `image/pwg-raster` and `image/urf` (Apple Raster), CUPS/macOS clients rasterise on their side and send pre-rendered raster streams, removing the external-binary dependency for the common path and enabling fully driverless (no-PPD) queue setup on macOS. PDF stays as a fallback format.

Key facts verified during planning:
- The existing `Filter` interface (`ToRaster(ctx, dpi, data) ([]image.Image, error)`, `ippsrv/filter.go`) is exactly the shape a raster decoder needs → integrate as a magic-byte-sniffing filter; **zero changes** to `basePrinter.Print`, `Driver`, or the composer.
- The existing compose loop (`printer.go:224-231`: `IsDocument` → threshold, else Floyd-Steinberg) is content-lossless for already-1-bit pages and correctly dithers gray/color pages; `ResizeToFit` is a no-op at width ≤ 384.
- Printer: 384 px wide, 203 dpi. `document-format` is not captured from requests; body is sniffed — so route by magic bytes.
- Header layouts below were **empirically verified on this Mac** by generating streams with `/usr/sbin/cupsfilter -m image/pwg-raster|image/urf -p ippsrv/ppd/LX-D02.ppd` (works; needs `-p` and explicit `-o Resolution=203dpi`, otherwise emits 100dpi).
- goipp v1.2.0 has `goipp.Resolution{Xres, Yres, Units: goipp.UnitsDpi}` + `TagResolution`, usable with the existing `adder` helper (`ipp_utils.go`).
- brutella/dnssd v1.2.14 does **not** support DNS-SD subtypes → skip `_universal._sub._ipp._tcp`; macOS Printers & Scanners browses `_ipp._tcp` directly (only iOS strictly needs the subtype — accepted limitation, document in a comment).

Decisions: media advertised at **48mm printable width** (client rasters arrive at exactly 384px, no rescaling) — flagged: switch to 58mm names if label-stock-matching names matter more than sharpness. mDNS/attribute work builds on the `bonjour` branch work.

## Milestone 1 — decoder + sniffing filter (server accepts pushed raster)

### New package `cupsraster/` (repo root)

Files: `cupsraster.go` (doc, `Format` enum, `Detect(data []byte) Format`, `Decode(r io.Reader) ([]image.Image, error)` dispatch), `pwg.go`, `urf.go`, `rle.go`, tests + `testdata/`.

API: `Detect`, `Decode`, `DecodePWG`, `DecodeURF`. Output per page: bpp 1 → `image.Gray` (bit-expand), bpp 8 sGray → `image.Gray`, bpp 24 sRGB → `image.NRGBA` (decoded defensively even though color is not advertised).

**1-bit polarity is per color space — do not generalise one rule:**
- PWG ColorSpace 3 ("black"/K, ink semantics): bit 1 = black, bit 0 = white.
- PWG ColorSpace 18 (sgray) and URF colorSpace 0 (sGray/luminance) at bpp 1: luminance semantics — bit 0 = black, bit 1 = white (inverse of K).
- Fixture tests MUST assert black text renders black for **both** PWG black_1 and URF 1-bit mono (generate both fixtures from the same input document and compare decoded pixels).

**PWG raster** (PWG 5102.4): 4-byte sync `"RaS2"` once, then per page 1796-byte big-endian header + RLE. Detect: `"RaS2"` at 0 **and** `"PwgRaster\0"` at 4 (guards against little-endian CUPS-raster confusion; same rule as `/usr/share/cups/mime/apple.types`). Verified header offsets (u32 BE): HWResolution X/Y @ 276/280, **Width/Height @ 372/376**, BitsPerColor @ 384, **BitsPerPixel @ 388**, **BytesPerLine @ 392**, ColorOrder @ 396 (must be 0), **ColorSpace @ 400** (3=black, 18=sgray, 19=srgb).

**Apple URF**: `"UNIRAST\x00"` + u32 BE page count; per page 32-byte header: bitsPerPixel u8 @ 0 (support 1/8/24 — macOS emitted 1 with our mono PPD), colorSpace u8 @ 1 (0=sGray, 1=sRGB), width u32 @ 12, height @ 16, dpi @ 20. No BytesPerLine — compute `(width*bpp+7)/8`.

**Shared RLE** (`rle.go`): per line group: 1 byte lineRepeat (line spans repeat+1 rows); then runs of pixel groups (group = `max(1, bpp/8)` bytes) until bytesPerLine produced: control `0..127` → next group repeated c+1 times; `129..255` → `257-c` literal groups; `0x80` → treat defensively as "fill rest of line with white, end line" (URF uses it; reserved in PWG — verify against fixtures). Because "white" depends on color space (0x00 in PWG K 1-bit, 0xFF in sGray/sRGB/URF mono), the RLE routine must take an explicit `fill byte` (or polarity) parameter from the per-format caller — it cannot derive it from bpp/bytesPerLine alone. Test the 0x80 path with both fill values. Include an unexported `encodePage` for round-trip tests.

Guards: reject zero/absurd dimensions (cap ~32768 and total-pixel cap), BytesPerLine consistency, truncated-stream errors naming the page index.

### Integration (`ippsrv`)

- `ippsrv/filter.go`: add `rasterSniffFilter{fallback Filter}` — `Detect != FormatUnknown` → `cupsraster.Decode`, else delegate; `Type() = "raster+" + fallback.Type()`.
- `ippsrv/printer.go:123` (`WrapDriver`): default filter becomes `&rasterSniffFilter{fallback: &imageMagickFilter{}}`.
- `ippsrv/job.go` `createJobFromRequest`: also capture `document-format` (via existing `extractValue[goipp.String]`) onto a new `Job` field for logging/dumps only.

## Milestone 2 — advertisement (driverless setup)

- `ippsrv/ipp_utils.go`: constants `ippImagePWGRaster = "image/pwg-raster"`, `ippImageURF = "image/urf"`.
- `ippsrv/ipp.go` `printerAttributes`: `document-format-supported` → **rasters only** (`image/pwg-raster`, `image/urf`), `document-format-default` → `image/pwg-raster`. **Do not advertise `application/pdf`**: CUPS driverless queues cost-optimise toward PDF passthrough when the destination lists PDF, which would defeat client-side rasterisation. The server still *accepts* PDF silently — the sniffing filter's magick fallback is unchanged and nothing validates `document-format` — so scripts pushing PDF keep working. (If a real client ever needs PDF advertised, add an `ippsrv` option then; don't pre-build it.) Add `pwg-raster-document-resolution-supported` (TagResolution, 203×203dpi), `pwg-raster-document-type-supported` (TagKeyword: `black_1`, `sgray_8` — mono/grayscale only; note PWG type keywords are bits-per-**color**, so 24-bit RGB would be `srgb_8`, never "srgb_24"), `pwg-raster-document-sheet-back` (`normal`), `urf-supported` (TagKeyword: `V1.4`, `W8`, `RS203` — no `SRGB24`: don't invite color rasters for a 1-bit printer; decoder still accepts them defensively).
- Minimal media collections (required here, not deferred — `lpadmin -m everywhere` and macOS driverless setup commonly query collections, not just keyword names): `media-size-supported` and `media-col-database`/`media-col-default` built with `goipp.Collection`, one entry per label size. Structure: `media-size` contains **only** `x-dimension`/`y-dimension` (hundredths of mm, 48mm → 4800); the zero margins are separate sibling members of `media-col` — `media-top-margin`, `media-bottom-margin`, `media-left-margin`, `media-right-margin` (integers, 0) — never inside `media-size`. Implementation note: every `goipp.Collection` member attribute must be **named** — goipp rejects collection members with empty names at encode time, so malformed collections surface only when the response is encoded, not when built.
- `ippsrv/printer.go`: `MediaSupported`/`MediaDefault` → PWG 5101.1 self-describing names at printable width: `om_label-48x100mm_48x100mm` (default), `om_label-48x40mm_48x40mm`, `om_label-48x32mm_48x32mm`, `om_label-48x60mm_48x60mm` (current `roll_57mm` is not parseable by `lpadmin -m everywhere`).
- `ippsrv/bonjour.go` `txtRecord`: `pdl = "image/urf,image/pwg-raster"` (no PDF — see above), add `URF = "V1.4,W8,RS203"`; comment noting the skipped `_universal` subtype (dnssd library limitation, iOS-only impact).
- Deferred follow-up (iterate with protocol dumps if queue setup stalls): richer media-col fields (margins, media-type keywords), color advertising (`srgb_8`/`SRGB24`) if ever wanted, subtype workaround for iOS.

## Tests

- `cupsraster`: RLE round-trips via internal encoder (bpp 1/8/24, lineRepeat > 0, literal/run mixes, multi-page, truncation errors); decode checked-in fixtures generated with `cupsfilter` (command above) asserting dimensions/dpi from headers **and 1-bit polarity per color space** (black text must decode black in both PWG black_1 and URF mono fixtures of the same document). Plain `testing`, style of `bitmap/*_test.go` and root `raster_test.go`.
- `ippsrv`: sniffing filter routes raster → decoder, everything else → fake fallback (table test); `printerAttributes` contains new formats/attributes **and the full response round-trips through `Message.Encode`** (encode to bytes, not just inspect the attribute list — this is what catches malformed/unnamed collection members early); **invert** `bonjour_test.go:35-36` (TXT must now contain `URF` and raster `pdl` entries); update media assertions in tests to the new PWG names.

## Verification

1. `go test ./...`, `go build ./...`.
2. **M1 gate**: run `tp server -dry`; `ipptool -tv -f cupsraster/testdata/doc.pwg ipp://<host>.local.:6310/printers/default print-job.test` (repeat with `.urf`); prove `magick` isn't invoked (temporarily rename it out of PATH). Inspect `-dumpdir` output.
3. **M2 gate**: `lpadmin -p tq -E -v ipp://<host>.local.:6310/printers/default -m everywhere && lp -d tq file.pdf` — verify the server receives `image/pwg-raster` at width 384 (protocol dumps).
4. macOS Settings → Printers & Scanners → Add Printer → should set up **without asking for a PPD** (driverless via URF TXT); print a test page; verify received format/width in dumps.
5. Regression: push a PDF directly (`ipptool -f file.pdf ... print-job.test`) — still prints via the magick fallback even though PDF is no longer advertised.
6. Confirm in the dumps that the `-m everywhere` queue actually sends `image/pwg-raster` (the whole point — if it somehow still sends PDF, investigate the generated PPD's cupsFilter2 entries).

## Risks

- `0x80` RLE control-byte semantics differ between URF and PWG (defensive fill-white handling; confirm with fixtures).
- Media width: if clients still raster at unexpected widths, iterate using dumps; composer downscaling is the safety net.
- macOS `cupsfilter` fixture quirk: emits 100dpi unless `-o Resolution=203dpi` passed.
- iOS AirPrint won't discover the printer without the `_universal` subtype (library limitation, out of scope).
- The exact URF TXT value `V1.4,W8,RS203` (grayscale-only, no SRGB24) is unvalidated against real macOS behaviour — the dump-based verification gate (step 4/6) is where it gets confirmed; if macOS balks at a colorless URF printer, adding `SRGB24` back (with the decoder's existing sRGB support) is the one-line fallback.
