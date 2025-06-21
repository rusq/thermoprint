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
