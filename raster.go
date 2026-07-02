package thermoprint

import (
	"fmt"
	"image"
	"log/slog"

	"github.com/rusq/thermoprint/bitmap"
)

type GenericRasteriser struct {
	Width          int
	Dpi            int
	LinesPerPacket int
	PrefixFunc     func(packetIndex int) []byte // returns 55 m n
	Terminator     byte                         // 00
	DitherFunc     bitmap.DitherFunc            // optional dither function
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
	SetDitherFunc(fn bitmap.DitherFunc)
}

func (r *GenericRasteriser) DPI() int {
	return r.Dpi
}

func (r *GenericRasteriser) LineWidth() int {
	return r.Width
}

func (r *GenericRasteriser) SetDitherFunc(fn bitmap.DitherFunc) {
	if fn == nil {
		r.DitherFunc = bitmap.DFloydSteinberg // reset to default if nil
	} else {
		r.DitherFunc = fn
	}
}

func (r *GenericRasteriser) ResizeAndDither(src image.Image, gamma float64, autoDither bool) image.Image {
	dfn := bitmap.DitherDefault
	if r.DitherFunc != nil {
		dfn = r.DitherFunc
	}

	resized := bitmap.ResizeToFit(src, r.Width)
	if autoDither && bitmap.IsDocument(resized, 50, 200) {
		slog.Info("Image is a document, skipping dithering", "autodither", autoDither, "width", r.Width, "height", resized.Bounds().Dy())
		// If the image is not a document, apply dithering
		return resized
	}
	return dfn(resized, gamma)
}

func (r *GenericRasteriser) Serialise(img image.Image) ([][]byte, error) {
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

	rasteriseLine := func(relY int) []byte {
		lineBytes := make([]byte, lineWidthBytes)
		if relY >= height {
			return lineBytes
		}
		for x := range lineWidthPixels {
			if x >= width {
				break
			}
			bit := bitmap.PixelBit(img, bounds.Min.X+x, bounds.Min.Y+relY, r.Threshold)
			if bit {
				lineBytes[x/8] |= (1 << (7 - (x % 8)))
			}
		}
		return lineBytes
	}

	serializedHeight := height
	if serializedHeight%linesPerMsg != 0 {
		serializedHeight += linesPerMsg - serializedHeight%linesPerMsg
	} else {
		serializedHeight += linesPerMsg
	}

	numPackets := serializedHeight / linesPerMsg
	packets := make([][]byte, 0, numPackets)

	for packetIndex := range numPackets {
		row := make([]byte, 0, msgPayloadSz)

		row = append(row, r.PrefixFunc(packetIndex)...)

		for line := range linesPerMsg {
			relY := packetIndex*linesPerMsg + line
			row = append(row, rasteriseLine(relY)...)
		}

		row = append(row, r.Terminator) // terminating byte

		packets = append(packets, row)
	}

	return packets, nil
}

// Enumerate converts the raw data to printer specific packets ready to be sent
// to printer.
func (r *GenericRasteriser) Enumerate(data [][]byte) ([][]byte, error) {
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
