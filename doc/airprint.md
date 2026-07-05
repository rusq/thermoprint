# AirPrint / Bonjour / raster notes for agents

Hard-won empirical knowledge about the IPP server's discovery and raster
paths.  Read this before touching `ippsrv/bonjour.go`, `ippsrv/airprint.go`,
`ippsrv/ipp.go` attributes, or the `cupsraster` package.

## Testing without printer hardware

- `tp server -dry` runs the server without Bluetooth (`-dry` is a global
  flag, or `DRY_RUN=1`). The driver writes `preview_*.png` instead of
  printing.
- `dns-sd -B _ipp._tcp local.` browses the printer; `dns-sd -L "<instance>"
  _ipp._tcp local.` shows SRV/TXT.
- `ipptool -tv [-f file] ipp://<host>:6310/printers/default
  get-printer-attributes.test` (or `print-job.test`) probes like macOS does.
  Strict-test FAILs on optional attributes we don't emit are expected.
- `lpadmin -p tq -E -v ipp://localhost:6310/printers/default -m everywhere`
  creates a driverless CUPS queue; `lp -d tq file.pdf` makes the client
  rasterise. Clean up with `lpadmin -x tq`.
- Goodbye packets only go out on graceful SIGINT; a hard kill leaves a stale
  mDNSResponder cache entry (clear with `sudo killall -HUP mDNSResponder`).
- Run the server with `-v -dumpdir <dir>` to capture the exact IPP requests
  clients send.

## Discovery / AirPrint classification

- macOS "Printers & Scanners" lists any `_ipp._tcp` service, but classifies
  a printer as **driverless (AirPrint)** only if it answers the
  **`_universal._sub._ipp._tcp` subtype browse**. Without it, macOS demands
  a driver and iOS does not show the printer at all.
- The subtype answer must be a PTR record *owned by* the subtype name whose
  rdata is the **parent-type** instance name (`<Instance>._ipp._tcp.local.`,
  RFC 6762 §7.1). Registering a second dnssd service *typed* as the subtype
  produces rdata under the subtype name, which mDNSResponder silently
  discards — do not retry that approach.
- `github.com/brutella/dnssd` (v1.2.14, latest) has **no subtype support**;
  the supplementary responder in `ippsrv/airprint.go` (miekg/dns, port 5353
  coexistence by binding the multicast group address) serves exactly that
  one PTR record. SRV/TXT/A resolution flows through the main responder.
- Verification: `dns-sd -B _ipp._tcp,_universal local.` must list the
  printer with type `_ipp._tcp`; `dns-sd -q
  _universal._sub._ipp._tcp.local. PTR IN` shows the raw rdata (compare with
  the Epson/HP entries on the LAN).
- `grandcat/zeroconf` (used by the abandoned `mdns` branch) fails same-host
  discovery on macOS; keep using brutella/dnssd for the main advertisement.
- TXT keys real AirPrint printers carry (all present in `txtRecord`):
  `product`, `usb_MFG`, `usb_MDL`, `kind`, `Scan`, `Fax`, `PaperMax`,
  `URF`, `pdl`, `rp`, `ty`, `note`, `adminurl`, `UUID`, `Color`, `Duplex`,
  `qtotal`, `txtvers`, `priority`. (`TLS` only with TLS support.)
- **SRGB24 in URF is NOT required** for AirPrint: the mono HP LaserJet
  M148dw advertises `URF=V1.4,CP99,W8,OB10,PQ3-4-5,DM1,IS1,MT1-3-5,RS600`
  without it. We stay grayscale-only (`W8`) on purpose.
- The `URF` TXT key and the `urf-supported` IPP attribute must agree — both
  come from `urfSupported()` in `ippsrv/bonjour.go`.

## Raster formats (cupsraster package)

- Only raster formats are advertised (`document-format-supported`, TXT
  `pdl`): listing `application/pdf` makes CUPS driverless queues pass PDFs
  through unchanged, defeating client-side rasterisation. PDF is still
  *accepted* silently — the sniffing filter (`ippsrv/filter.go`) falls back
  to ImageMagick for non-raster data.
- **1-bit polarity is per color space** and macOS deviates from a naive
  spec reading: PWG ColorSpace 3 (K): bit 1 = black; **URF 1-bit: bit 1 =
  black too** (ink semantics despite the sGray colorspace — verified against
  real macOS output; a wrong guess here shows up as a 100% inverted page in
  `cupsraster/fixture_test.go`).
- Media is advertised at the **printable** width (48mm, `om_label-48x...`),
  not the 58mm stock width, so clients rasterise at exactly ~384px. With
  this, a driverless client sends e.g. 383×799px @ 203dpi 8-bit gray.
- Fixture generation (macOS): `cupsfilter -i image/png -m image/pwg-raster
  -p ippsrv/ppd/LX-D02.ppd file.png` (same for `image/urf`). Caveats: needs
  `-p`; **always emits 100dpi** no matter what `-o Resolution=` says; a
  letter-sized PDF input yields a blank label crop — feed a PNG instead.
- Which format CUPS picks per job (PWG vs URF) is its internal cost choice;
  both share the RLE decoder, either is fine.

## Known remaining gaps

- `printer-location` / `printer-more-info` attributes not emitted;
  `requested-attributes` is ignored (full set always returned).
- Job-ID collision: `JobID = time.Now().Unix()` — two jobs within the same
  second collide ("job already exists").
- No TLS, so no `_ipps._tcp` / "Secure AirPrint".
