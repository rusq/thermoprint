// Package bitmap provides bitmap image manipulation funcitons.
package bitmap

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

const (
	// DefaultThreshold is the default threshold for dark pixels.
	DefaultThreshold = 128
	// DefaultGamma is a special value that instructs to use the default gamma
	// for the diffuse algorithm.
	DefaultGamma = 0.0
)

func PixelBit(img image.Image, x, y int, threshold uint8) bool {
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
	gray := ColorToGray(c)
	return gray < threshold // dark pixels are "on"
}

func ColorToGray(c color.Color) uint8 {
	if gray, ok := c.(color.Gray); ok {
		return gray.Y
	}
	r, g, b, _ := c.RGBA()
	gray := (299*r + 587*g + 114*b) / 1000
	return uint8(gray >> 8)
}

func IsDocument(img image.Image, darkThreshold, lightThreshold uint8) bool {
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
