package printers

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

var TestPatterns = map[string]func(int) image.Image{
	"LastLineTest":    LastLineTest,
	"MillimeterLines": MillimeterLines,
	"Sinusoidal":      SinusoidalPattern,
}

// LastLineTest generates 8 lines each of which is 2 pixels high shifted by one pixel to the right,
// so that thermal unit is expected to print 4 times.
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
func LastLineTest(maxX int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, maxX, 16))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for y := 0; y < 8; y++ {
		for x := 0; x < maxX; x++ {
			if (x+y)%2 == 0 {
				img.Set(x, y*2, color.Black)
				img.Set(x, y*2+1, color.Black)
			}
		}
	}
	return img
}

// MillimeterLines draws a running pattern of millimeter lines.
// Each horizontal line is 8 dots wide, and 1 dot high.  Each horizontal line is
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
func MillimeterLines(maxX int) image.Image {
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

func SinusoidalPattern(maxX int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, maxX, 64)) // 64 lines of 1 pixel each
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for x := 0; x < maxX; x++ {
		y := int(32 + 30*math.Sin(float64(x)*2*math.Pi/100)) // Sinusoidal wave with amplitude 30
		if y >= 0 && y < img.Bounds().Dy() {
			img.Set(x, y, color.Black)
		}
	}
	return img
}
