package thermoprint

import (
	"bytes"
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

var TestImagePatterns = map[string]func(int) image.Image{
	"RunningLinesImage": TestImgRunningLines,
	"Millimetres":       TestImgMillimetres,
	"Sine":              TestImgSine,
}

var TestBufferPatterns = map[string]func(int) [][]byte{
	"BinaryPattern":   BufferBinaryPattern,
	"FirstPacketRows": BufferFirstPacketRows,
}

const (
	firstPacketRowsSeparatorPackets = 8
	firstPacketRowsTrailingPackets  = 24
)

// TestImgRunningLines generates 8 lines each of which is 2 pixels high shifted by one pixel to the right,
// so that thermal unit is expected to print 4 times.
//
// Thermal unit prints 2 rows of pixels at once, 384 pixels in a row.
//
// The output looks like this:
//
//	| | | |
//	 | | | |
//	  | | | |
//	   | | | |
//	    | | | |
//	     | | | |
//	      | | | |
//	       | | | |
func TestImgRunningLines(maxX int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, maxX, 16))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for y := range 8 {
		for x := range maxX {
			if (x+y)%2 == 0 {
				img.Set(x, y*2, color.Black)
				img.Set(x, y*2+1, color.Black)
			}
		}
	}
	return img
}

// TestImgMillimetres draws a running pattern of millimeter lines.
// Each horizontal line is 8 dots wide (1mm), and 1 dot high (1/8 mm).  Each horizontal line is
// repeated every 40 dots, so that the first line is at 0, the second at 40, the third at 80,
// and so on, until the maximum X coordinate is reached.
// THe output looks like this:
//
//	 0 1 ..  40 ..  80 .. 120 .. 160 .. 200 .. 240 .. 280 .. 320 .. 360 .. 400
//		--      -- ...
//		  --      -- ...
//		    --      -- ...
//		      --      --
//		        --      --
//		          --      --
//
// etc.
func TestImgMillimetres(maxX int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, maxX, 384/8)) // 48 lines of 8 pixels each
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := y * 8; x < maxX; x += 40 {
			for x1 := x; x1 < x+8 && x1 < maxX; x1++ {
				img.Set(x1, y, color.Black)
			}
		}
	}
	return img
}

func TestImgSine(maxX int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, maxX, 64)) // 64 lines of 1 pixel each
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for x := range maxX {
		y := int(32 + 30*math.Sin(float64(x)*2*math.Pi/100)) // Sinusoidal wave with amplitude 30
		if y >= 0 && y < img.Bounds().Dy() {
			img.Set(x, y, color.Black)
		}
	}
	return img
}

func BufferBinaryPattern(width int) [][]byte {
	// width is given in pixels, we need to divide it by 8 and multiply by 2 as
	// it we are expected to send one []byte slice per 2 lines. So, we divide by 4
	width /= 4
	var ret = make([][]byte, 256)
	for i := range ret {
		v := uint8(i)
		ret[i] = bytes.Repeat([]byte{v}, width)
	}
	return ret
}

// BufferFirstPacketRows returns a raw diagnostic pattern for checking whether
// both pixel rows in the first printer packet are printed. The first packet
// has a black left half on its upper row and a black right half on its lower
// row. An identical control packet follows several blank packets, with more
// blank packets after it to feed both markers into view.
//
// Two identical split markers mean packet zero printed correctly. A missing
// left or right segment in the first marker means its upper or lower pixel row,
// respectively, was lost. If only the control marker appears, packet zero was
// lost entirely.
func BufferFirstPacketRows(width int) [][]byte {
	lineBytes := width / 8
	packetBytes := lineBytes * 2
	marker := make([]byte, packetBytes)

	half := lineBytes / 2
	for i := 0; i < half; i++ {
		marker[i] = 0xff
	}
	for i := lineBytes + half; i < packetBytes; i++ {
		marker[i] = 0xff
	}

	packets := make([][]byte, 0, 2+firstPacketRowsSeparatorPackets+firstPacketRowsTrailingPackets)
	packets = append(packets, marker)
	for range firstPacketRowsSeparatorPackets {
		packets = append(packets, make([]byte, packetBytes))
	}
	packets = append(packets, bytes.Clone(marker))
	for range firstPacketRowsTrailingPackets {
		packets = append(packets, make([]byte, packetBytes))
	}
	return packets
}
