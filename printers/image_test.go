package printers

import (
	"image"
	"os"
	"testing"

	_ "image/jpeg"
	"image/png"
)

func openImage(t *testing.T, filename string) image.Image {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatalf("failed to open image file %s: %v", filename, err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("failed to decode image file %s: %v", filename, err)
	}
	return img
}

func saveImage(t *testing.T, img image.Image, filename string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatalf("failed to create image file %s: %v", filename, err)
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		t.Fatalf("failed to encode image to file %s: %v", filename, err)
	}
}

func Test_resizeAndDither(t *testing.T) {
	type args struct {
		// img         image.Image
		targetWidth int
		ditherFn    func(image.Image) image.Image
	}
	tests := []struct {
		name   string
		input  string
		args   args
		output string
	}{
		{
			name:   "Resize and dither",
			input:  "../media/harold.jpg",
			args:   args{targetWidth: 384, ditherFn: ditherimg},
			output: "../media/harold_out.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := resizeAndDither(openImage(t, tt.input), tt.args.targetWidth, tt.args.ditherFn)
			saveImage(t, out, tt.output)
		})
	}
}
