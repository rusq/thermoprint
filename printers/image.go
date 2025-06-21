package printers

import (
	"image"
	"image/color"

	"github.com/makeworld-the-better-one/dither/v2"
	"golang.org/x/image/draw"
)

const (
	DefaultThreshold = 128 // Default threshold for dark pixels
)

func resize(img image.Image, targetWidth int) image.Image {
	var resized draw.Image
	if img.Bounds().Dx() <= targetWidth {
		// We don't upscale, but place the image on a white canvas in the upper
		// left corner
		targetHeight := img.Bounds().Dy()
		resized = image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		// fill canvas with white
		white := image.NewUniform(color.White)
		draw.Draw(resized, resized.Bounds(), white, image.Point{}, draw.Src)
		// Copy the original image onto the resized canvas in left upper corner
		draw.Copy(resized, image.Point{0, 0}, img, img.Bounds(), draw.Src, nil)
	} else {
		// Resize the image to the target width while maintaining aspect ratio
		targetHeight := (img.Bounds().Dy() * targetWidth) / img.Bounds().Dx()
		resized = image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		draw.CatmullRom.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)
	}
	return resized
}

// ditherimg is the default dither function used in the rasteriser.
func ditherimg(img image.Image) image.Image {
	return dFloydSteinberg(img)
}

func dFloydSteinberg(img image.Image) image.Image {
	dithered := image.NewPaletted(img.Bounds(), []color.Color{color.Black, color.White})
	draw.FloydSteinberg.Draw(dithered, dithered.Bounds(), img, image.Point{})
	return dithered
}

func dStucki(img image.Image) image.Image {
	dithered := image.NewRGBA(img.Bounds())
	d := dither.NewDitherer([]color.Color{color.Black, color.White})
	d.Matrix = dither.Atkinson
	d.Draw(dithered, dithered.Bounds(), img, image.Point{})
	return dithered
}

func dBayer(img image.Image) image.Image {
	dithered := image.NewPaletted(img.Bounds(), []color.Color{color.Black, color.White})
	d := dither.NewDitherer([]color.Color{color.Black, color.White})
	d.Mapper = dither.Bayer(8, 8, 1.0) // 8x8 Bayer matrix
	d.Draw(dithered, dithered.Bounds(), img, image.Point{})
	return dithered
}

func resizeAndDither(img image.Image, targetWidth int, ditherFn func(image.Image) image.Image) image.Image {
	return ditherFn(resize(img, targetWidth))
}

type Raster struct {
	Width          int
	Dpi            int
	LinesPerPacket int
	PrefixFunc     func(packetIndex int) []byte      // returns 55 m n
	Terminator     byte                              // 00
	DitherFunc     func(img image.Image) image.Image // optional dither function
	Threshold      uint8                             // threshold for dark pixels, default is 128
}

type Rasteriser interface {
	Rasterise(src image.Image) [][]byte                 // returns a slice of byte slices, each representing a packet
	DPI() int                                           // returns the DPI of the rasteriser
	LineWidth() int                                     // returns the line width in pixels
	SetDitherFunc(fn func(img image.Image) image.Image) // sets the dither function
}

func DitherThresholdFn(threshold uint8) func(img image.Image) image.Image {
	return func(img image.Image) image.Image {
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

var LXD02Rasteriser = &Raster{
	Width:          384, // 48 bytes
	Dpi:            203, // 203 DPI
	LinesPerPacket: 2,   // 2 lines per packet
	PrefixFunc: func(packetIndex int) []byte {
		m := byte((packetIndex >> 8) & 0xFF)
		n := byte(packetIndex & 0xFF)
		return []byte{0x55, m, n} // 55 m n
	},
	Terminator: 0x00,             // 00
	Threshold:  DefaultThreshold, // default threshold for dark pixels
	DitherFunc: dFloydSteinberg,  // default dither function
}

func (r *Raster) DPI() int {
	return r.Dpi
}

func (r *Raster) LineWidth() int {
	return r.Width
}

func (r *Raster) SetDitherFunc(fn func(img image.Image) image.Image) {
	if fn == nil {
		r.DitherFunc = dFloydSteinberg // reset to default if nil
	} else {
		r.DitherFunc = fn
	}
}

func (r *Raster) Rasterise(src image.Image) [][]byte {
	var (
		msgPrefixSz     = len(r.PrefixFunc(0)) // 55 m n
		msgTerminatorSz = 1                    // 00

		lineWidthPixels = r.Width
		lineWidthBytes  = lineWidthPixels / 8
		linesPerMsg     = r.LinesPerPacket

		msgDataSz    = lineWidthBytes * linesPerMsg
		msgPayloadSz = msgPrefixSz + msgDataSz + msgTerminatorSz // 55 m n + data + 00
	)

	dfn := ditherimg
	if r.DitherFunc != nil {
		dfn = r.DitherFunc
	}

	img := resizeAndDither(src, lineWidthPixels, dfn)

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width > lineWidthPixels {
		panic("image width exceeds 384px limit")
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

	return packets
}

func rasterizeImage(src image.Image) [][]byte {
	if src == nil {
		return nil
	}

	// Use the LXD02 rasteriser by default
	return LXD02Rasteriser.Rasterise(src)
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
