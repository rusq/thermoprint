package bitmap

import (
	"image"
	"image/color"
	"testing"
)

func resizeAndDither(img image.Image, targetWidth int, ditherFn DitherFunc) image.Image {
	return ditherFn(ResizeToFit(img, targetWidth), DefaultGamma)
}

func Test_resizeAndDither(t *testing.T) {
	type args struct {
		targetWidth int
		ditherFn    DitherFunc
	}
	tests := []struct {
		name       string
		input      image.Image
		args       args
		wantBounds image.Rectangle
	}{
		{
			name:       "Resize and dither",
			input:      makeGradient(96, 24),
			args:       args{targetWidth: 384, ditherFn: DStucki},
			wantBounds: image.Rect(0, 0, 384, 24),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := resizeAndDither(tt.input, tt.args.targetWidth, tt.args.ditherFn)
			if got := out.Bounds(); got != tt.wantBounds {
				t.Fatalf("resizeAndDither bounds = %v, want %v", got, tt.wantBounds)
			}
			assertBlackWhite(t, out)
		})
	}
}

func makeGradient(width, height int) image.Image {
	img := image.NewGray(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			v := uint8(x * 255 / (width - 1))
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return img
}

func assertBlackWhite(t *testing.T, img image.Image) {
	t.Helper()
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			gray := ColorToGray(img.At(x, y))
			if gray != 0 && gray != 255 {
				t.Fatalf("pixel at (%d,%d) = %d, want black or white", x, y, gray)
			}
		}
	}
}
