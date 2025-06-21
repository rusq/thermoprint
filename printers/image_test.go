package printers

import (
	"bytes"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"reflect"
	"testing"
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
			args:   args{targetWidth: 384, ditherFn: dStucki},
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

// makeCheckers creates a checkerboard image of the specified width and height.
func makeCheckers(t *testing.T, width, height int) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if (x+y)%2 == 0 {
				img.Set(x, y, image.White)
			} else {
				img.Set(x, y, image.Black)
			}
		}
	}
	return img
}

func TestRaster_Rasterise(t *testing.T) {
	type args struct {
		src image.Image
	}
	tests := []struct {
		name   string
		fields Raster
		args   args
		want   [][]byte
	}{
		{
			name:   "Rasterise image",
			fields: *LXD02Rasteriser,
			args: args{
				src: makeCheckers(t, 384, 4), // Create a checkerboard image
			},
			want: [][]byte{
				append(append(append([]byte{0x55, 0x00, 0x00}, bytes.Repeat([]byte{0b01010101}, 48)...), bytes.Repeat([]byte{0b10101010}, 48)...), 0x00),
				append(append(append([]byte{0x55, 0x00, 0x01}, bytes.Repeat([]byte{0b01010101}, 48)...), bytes.Repeat([]byte{0b10101010}, 48)...), 0x00),
			},
		},
		{
			name:   "rasterise text",
			fields: *LXD02Rasteriser,
			args: args{
				src: openImage(t, "../media/rasterised.png"), // Use a sample image
			},
			want: [][]byte{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &tt.fields
			got := r.Rasterise(tt.args.src)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Raster.Rasterise() = [% x], want [% x]", got, tt.want)
				if err := os.WriteFile("test_rasterised.bin", bytes.Join(got, []byte{'\n'}), 0644); err != nil {
					t.Fatalf("failed to write output file: %v", err)
				}
			}
		})
	}
}
