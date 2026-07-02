package bitmap

import (
	"image"
	"testing"
)

func TestPixelBitNonZeroOriginBounds(t *testing.T) {
	img := image.NewGray(image.Rect(10, 20, 12, 22))
	img.Set(10, 20, image.Black)

	if !PixelBit(img, 10, 20, DefaultThreshold) {
		t.Fatal("PixelBit returned false for black pixel inside non-zero-origin bounds")
	}
	if PixelBit(img, 0, 0, DefaultThreshold) {
		t.Fatal("PixelBit returned true for point outside non-zero-origin bounds")
	}
	if PixelBit(img, 12, 20, DefaultThreshold) {
		t.Fatal("PixelBit returned true for point on right edge outside bounds")
	}
}
