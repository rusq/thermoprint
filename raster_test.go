package thermoprint

import (
	"bytes"
	"image"
	"os"
	"reflect"
	"testing"

	"github.com/rusq/thermoprint/bitmap"
)

func TestRaster_Rasterise(t *testing.T) {
	type args struct {
		src        image.Image
		gamma      float64
		autoDither bool
	}
	tests := []struct {
		name    string
		fields  GenericRasteriser
		args    args
		want    [][]byte
		wantErr bool
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
			wantErr: false,
		},
		{
			name:   "rasterise text",
			fields: *LXD02Rasteriser,
			args: args{
				src: openImage(t, "media/rasterised.png"), // Use a sample image
			},
			want:    [][]byte{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &tt.fields
			got, err := r.Serialise(r.ResizeAndDither(tt.args.src, bitmap.DefaultGamma, tt.args.autoDither))
			if (err != nil) != tt.wantErr {
				t.Errorf("Raster.Rasterise() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Raster.Rasterise() = [% x], want [% x]", got, tt.want)
				if err := os.WriteFile("test_rasterised.bin", bytes.Join(got, []byte{'\n'}), 0644); err != nil {
					t.Fatalf("failed to write output file: %v", err)
				}
			}
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
