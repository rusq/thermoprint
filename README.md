# Thermal Bluetooth Printer LX-D02

Allows to print on a Bluetooth thermal printer LX-D02 from your computer.

Supports printing images and (somewhat) text and test patterns.

## Images
Resize, dither and print image:
```shell
thermoprint -i image.png
```
Selecting different dither method:
```shell
thermoprint -i image.png -dither stucki
```
The following dithering algorithms are supported:
- atkinson
- bayer
- floyd-steinberg
- stucki

Default is "atkinson".

## Text
Printing text:
```shell
ls -l | fold -w 49 | thermoprint -t -
```
If you don't want text to be resized, you can use `-crop` option, it will chop
off the image at the 384 pixel boundary.

```shell
thermoprint -crop -t "very long text that doesn't fit 58mm roll" 
```
## Test patterns
You can print test patterns to check printer quality:
```shell
thermoprint -pattern MillimeterLines
```

# Print server (AirPrint / IPP Everywhere)

`tp server` starts an IPP print server for the connected printer and
advertises it on the local network via Bonjour/DNS-SD, so it can be added
as a regular network printer — no drivers required.

```shell
tp server
```

By default it listens on all interfaces on port 6310 (`-addr :6310`).
Useful flags:

- `-addr host:port` — listen address. Binding a loopback address (e.g.
  `localhost:6310`) disables network discovery.
- `-no-mdns` — turn off the Bonjour/DNS-SD advertisement.
- `-dry` — dry run: jobs are rendered to `preview_*.png` files instead of
  the printer (no Bluetooth needed; handy for testing).
- `-dumpdir dir` — with `-v`, dump the IPP protocol exchanges for
  debugging.

Print jobs are expected as PWG Raster (`image/pwg-raster`) or Apple Raster
(`image/urf`) — the client rasterises the document, so the server host
needs no external tools.  PDF is also accepted as a fallback, in which case
the server converts it locally using ImageMagick (`magick` must be in the
`PATH`).

Supported label sizes: 48×32, 48×40, 48×60 and 48×100 mm (the printable
width of the 58 mm roll is 48 mm / 384 px at 203 dpi).

## AirPrint (macOS)

With the server running, open **System Settings → Printers & Scanners →
Add Printer, Scanner or Fax…**: "LX-D02 Thermal Printer" appears in the
list as an AirPrint printer.  Add it — no driver or PPD is asked for —
select the label size in the print dialog and print.

### Variable roll height on macOS

For continuous roll paper, create a custom paper size in the macOS print
dialog:

1. Open the app's print dialog and expand **Paper Size**.
2. Choose **Manage Custom Sizes…**.
3. Press **+** and create a size with width `48 mm` and a height between
   `20 mm` and `1000 mm`.
4. Set all non-printable margins to `0`.
5. Select that custom size before printing.

The printer advertises the 48 mm printable width, not the full 58 mm stock
width, so custom sizes should also use `48 mm` width.  Custom roll-height
jobs are trimmed at the bottom after rasterising, so selecting a taller page
acts as an upper bound; fixed label sizes still feed their full selected
height for die-cut stock alignment.

If you re-configure the server (or upgrade to a newer version), remove and
re-add the printer: macOS generates the printer description once, at add
time.

## IPP Everywhere (Linux / CUPS command line)

```shell
lpadmin -p lx-d02 -E -v ipp://<hostname>:6310/printers/default -m everywhere
lp -d lx-d02 document.pdf
```

The client-side CUPS filters rasterise the document and send it to the
server as PWG Raster.

## PPD fallback (classic queue)

If driverless setup is not an option, a classic queue can be created with
the bundled PPD.  This path sends PDF to the server, which then requires
ImageMagick on the server host:

```shell
lpadmin -p lx-d02 -E -v ipp://<hostname>:6310/printers/default \
    -P ippsrv/ppd/LX-D02.ppd
lp -d lx-d02 document.pdf
```

On macOS the PPD can also be selected manually when adding the printer
("Use → Other…" in the add-printer dialog).

See [doc/airprint.md](doc/airprint.md) for implementation notes and
troubleshooting (discovery, TXT records, raster formats).

# Using as a library

See pkg.go.dev for library functions.

# Credits

This is based on the work in this repository https://github.com/big-vl/catcombo,
which was used to understand the protocol, and used as a reference for document
images detection.

Reason I didn't use it directly as my printer works slightly differently - it
sends, what I called "hold"/"restart from" packages which are not handled in
the original library.

# References
- [Debug Bluetooth Applications on iOS](https://www.bluetooth.com/blog/a-new-way-to-debug-iosbluetooth-applications/) -
  used to trace communication between FunnyPrint and LX-D02.
- [Catcombo repository](https://github.com/big-vl/catcombo) by big-vl used to understand the protocol.
- [Thermal Printer LX-D02](https://www.aliexpress.com/item/1005006069849901.html) used in for this project.
