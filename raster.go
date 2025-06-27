package thermoprint

import (
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"

	"golang.org/x/image/draw"
)

type DitherFunc func(img image.Image, gamma float64) image.Image

type Raster struct {
	Width          int
	Dpi            int
	LinesPerPacket int
	PrefixFunc     func(packetIndex int) []byte // returns 55 m n
	Terminator     byte                         // 00
	DitherFunc     DitherFunc                   // optional dither function
	Threshold      uint8                        // threshold for dark pixels, default is 128
}

type Rasteriser interface {
	ResizeAndDither(img image.Image, gamma float64, autoDither bool) image.Image
	// Serialise should return a slice of byte slices that are sent to printer.
	Serialise(src image.Image) ([][]byte, error)
	// Enumerate prepares the raw data for printing running the packet func
	// for each byte slice and returning the data ready to be sent to printer.
	Enumerate(data [][]byte) ([][]byte, error)
	// DPI should return the DPI of the rasteriser.
	DPI() int
	// LineWidth should return the line width in pixels, i.e. for 203 dpi
	// thermal printer that uses 58mm paper, it is 384.
	LineWidth() int
	// SetDitherFunc should set the dither function.
	SetDitherFunc(fn DitherFunc)
}

func DitherThresholdFn(threshold uint8) DitherFunc {
	return func(img image.Image, _ float64) image.Image {
		if threshold == 0 {
			threshold = DefaultThreshold // default threshold for dark pixels
		}
		trg := image.NewPaletted(img.Bounds(), []color.Color{color.Black, color.White})
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				if pixelBit(img, x, y, threshold) {
					trg.SetColorIndex(x, y, 0) // black
				} else {
					trg.SetColorIndex(x, y, 1) // white
				}
			}
		}
		return trg
	}
}

func (r *Raster) DPI() int {
	return r.Dpi
}

func (r *Raster) LineWidth() int {
	return r.Width
}

func (r *Raster) SetDitherFunc(fn DitherFunc) {
	if fn == nil {
		r.DitherFunc = dFloydSteinberg // reset to default if nil
	} else {
		r.DitherFunc = fn
	}
}

func (r *Raster) ResizeAndDither(src image.Image, gamma float64, autoDither bool) image.Image {
	dfn := ditherimg
	if r.DitherFunc != nil {
		dfn = r.DitherFunc
	}

	resized := resize(src, r.Width)
	slog.Info("x", "autodither", autoDither)
	if autoDither && isDocument(resized, 50, 200) {
		// If the image is not a document, apply dithering
		return resized
	}
	return dfn(resized, gamma)
}

func (r *Raster) Serialise(img image.Image) ([][]byte, error) {
	var (
		msgPrefixSz     = len(r.PrefixFunc(0)) // 55 m n
		msgTerminatorSz = 1                    // 00

		lineWidthPixels = r.Width
		lineWidthBytes  = lineWidthPixels / 8
		linesPerMsg     = r.LinesPerPacket

		msgDataSz    = lineWidthBytes * linesPerMsg
		msgPayloadSz = msgPrefixSz + msgDataSz + msgTerminatorSz // 55 m n + data + 00
	)

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width > lineWidthPixels {
		return nil, fmt.Errorf("image size (%d) exceeds %d pixel limit for this rasteriser", width, lineWidthPixels)
	}

	rasteriseLine := func(img image.Image, y int) []byte {
		lineBytes := make([]byte, lineWidthBytes)
		for x := range lineWidthPixels {
			bit := pixelBit(img, bounds.Min.X+x, bounds.Min.Y+y, r.Threshold)
			if bit {
				lineBytes[x/8] |= (1 << (7 - (x % 8)))
			}
		}
		return lineBytes
	}

	// Pad height to even number for 2-line packets
	if height%2 != 0 {
		height++
	} else {
		height += 2 // ensure we have at least 2 lines for the last packet
	}

	numPackets := height / linesPerMsg
	packets := make([][]byte, 0, numPackets)

	for packetIndex := range numPackets {
		y0 := packetIndex * 2
		y1 := y0 + 1

		row := make([]byte, 0, msgPayloadSz)

		row = append(row, r.PrefixFunc(packetIndex)...)

		// First line (y0)
		lineBytes := rasteriseLine(img, y0)
		row = append(row, lineBytes...)

		// Second line (y1)
		lineBytes = rasteriseLine(img, y1)
		row = append(row, lineBytes...)

		row = append(row, r.Terminator) // terminating byte

		packets = append(packets, row)
	}

	return packets, nil
}

// Enumerate converts the raw data to printer specific packets ready to be sent
// to printer.
func (r *Raster) Enumerate(data [][]byte) ([][]byte, error) {
	var (
		msgPrefixSz     = len(r.PrefixFunc(0)) // 55 m n
		msgTerminatorSz = 1                    // 00

		lineWidthPixels = r.Width
		lineWidthBytes  = lineWidthPixels / 8
		linesPerMsg     = r.LinesPerPacket

		msgDataSz    = lineWidthBytes * linesPerMsg
		msgPayloadSz = msgPrefixSz + msgDataSz + msgTerminatorSz // 55 m n + data + 00
	)
	var ret = make([][]byte, len(data))
	for i, line := range data {
		if len(line) != msgDataSz {
			return nil, fmt.Errorf("corrupt raw data on line %d, length mismatch %d < %d", i, len(line), msgDataSz)
		}
		row := make([]byte, 0, msgPayloadSz)

		row = append(row, r.PrefixFunc(i)...)
		row = append(row, line...)
		row = append(row, r.Terminator)
		ret[i] = row
	}
	return ret, nil
}

func pixelBit(img image.Image, x, y int, threshold uint8) bool {
	if threshold == 0 {
		threshold = DefaultThreshold // default threshold for dark pixels
	}
	if y >= img.Bounds().Dy() {
		return false // padded line
	}
	if x >= img.Bounds().Dx() {
		return false // image narrower than 384px
	}

	c := img.At(x, y)
	gray := colorToGray(c)
	return gray < threshold // dark pixels are "on"
}

func colorToGray(c color.Color) uint8 {
	if gray, ok := c.(color.Gray); ok {
		return gray.Y
	}
	r, g, b, _ := c.RGBA()
	gray := (299*r + 587*g + 114*b) / 1000
	return uint8(gray >> 8)
}

func isDocument(img image.Image, darkThreshold, lightThreshold uint8) bool {
	if img == nil {
		return false
	}
	if darkThreshold == 0 {
		darkThreshold = 50
	}
	if lightThreshold == 0 {
		lightThreshold = 200
	}
	bounds := img.Bounds()
	dst := image.NewGray(img.Bounds())
	draw.Draw(dst, bounds, img, image.Point{}, draw.Src)
	// create histogram of pixel brightness
	histogram := make([]int, math.MaxUint8+1)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := dst.At(x, y).(color.Gray)
			histogram[c.Y]++
		}
	}
	// sum all dark pixels in the range [0, darkThreshold)
	var (
		darkPixelCount  float64
		lightPixelCount float64
		totalPixelCount float64
	)
	for i, count := range histogram {
		totalPixelCount += float64(count)
		if i < int(darkThreshold) {
			darkPixelCount += float64(count)
		} else if i >= int(lightThreshold) {
			lightPixelCount += float64(count)
		}
	}
	if totalPixelCount == 0 {
		return false // no pixels to analyze
	}

	return (darkPixelCount+lightPixelCount)/totalPixelCount > 0.85
}
