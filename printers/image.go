package printers

import (
	"image"
	"image/color"
)

func rasterizeImage(img image.Image) [][]byte {
	const (
		packetPrefixSize  = 3 // 55 m n
		packetDataSize    = 96
		packetTerminator  = 1 // 00
		lineWidthPixels   = 384
		lineWidthBytes    = lineWidthPixels / 8
		linesPerPacket    = 2
		packetPayloadSize = packetPrefixSize + packetDataSize + packetTerminator
	)

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width > lineWidthPixels {
		panic("image width exceeds 384px limit")
	}

	// Pad height to even number for 2-line packets
	if height%2 != 0 {
		height++
	}

	numPackets := height / linesPerPacket
	packets := make([][]byte, 0, numPackets)

	for packetIndex := 0; packetIndex < numPackets; packetIndex++ {
		y0 := packetIndex * 2
		y1 := y0 + 1

		row := make([]byte, 0, packetPayloadSize)
		m := byte((packetIndex >> 8) & 0xFF)
		n := byte(packetIndex & 0xFF)

		row = append(row, 0x55, m, n)

		// First line (y0)
		lineBytes := make([]byte, lineWidthBytes)
		for x := 0; x < lineWidthPixels; x++ {
			bit := pixelBit(img, bounds.Min.X+x, bounds.Min.Y+y0)
			if bit {
				lineBytes[x/8] |= (1 << (7 - (x % 8)))
			}
		}
		row = append(row, lineBytes...)

		// Second line (y1)
		lineBytes = make([]byte, lineWidthBytes)
		for x := 0; x < lineWidthPixels; x++ {
			bit := pixelBit(img, bounds.Min.X+x, bounds.Min.Y+y1)
			if bit {
				lineBytes[x/8] |= (1 << (7 - (x % 8)))
			}
		}
		row = append(row, lineBytes...)

		row = append(row, 0x00) // terminating byte

		packets = append(packets, row)
	}

	return packets
}

func pixelBit(img image.Image, x, y int) bool {
	if y >= img.Bounds().Dy() {
		return false // padded line
	}
	if x >= img.Bounds().Dx() {
		return false // image narrower than 384px
	}

	c := img.At(x, y)
	gray := colorToGray(c)
	return gray < 128 // dark pixels are "on"
}

func colorToGray(c color.Color) uint8 {
	r, g, b, _ := c.RGBA()
	gray := (299*r + 587*g + 114*b) / 1000
	return uint8(gray >> 8)
}
