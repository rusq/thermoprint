package thermoprint

import (
	"bytes"
	"image"
	"image/color"
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
				append(append([]byte{0x55, 0x00, 0x02}, bytes.Repeat([]byte{00}, 48*2)...), 0x00),
			},
			wantErr: false,
		},
		{
			name:   "rasterise generated pattern",
			fields: *LXD02Rasteriser,
			args: args{
				src: makeTextLikePattern(96, 16),
			},
			want:    nil,
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
			if tt.want == nil {
				assertRasterPackets(t, got, r)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Raster.Rasterise() = [% x], want [% x]", got, tt.want)
			}
		})
	}
}

func TestGenericRasteriserSerialisePadsOddHeight(t *testing.T) {
	r := testRasteriser(8, 2)
	img := image.NewGray(image.Rect(0, 0, 8, 1))
	for x := range 8 {
		img.Set(x, 0, image.Black)
	}

	got, err := r.Serialise(img)
	if err != nil {
		t.Fatalf("Serialise returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Serialise returned %d packets, want 1", len(got))
	}
	want := []byte{0x00, 0xff, 0x00, 0x00}
	if !bytes.Equal(got[0], want) {
		t.Fatalf("Serialise packet = [% x], want [% x]", got[0], want)
	}
}

func TestGenericRasteriserSerialiseNonZeroOrigin(t *testing.T) {
	r := testRasteriser(8, 2)
	shifted := image.NewGray(image.Rect(5, 7, 13, 9))
	zero := image.NewGray(image.Rect(0, 0, 8, 2))

	for y := range 2 {
		for x := range 8 {
			c := color.White
			if (x+y)%3 == 0 {
				c = color.Black
			}
			shifted.Set(5+x, 7+y, c)
			zero.Set(x, y, c)
		}
	}

	gotShifted, err := r.Serialise(shifted)
	if err != nil {
		t.Fatalf("Serialise shifted image returned error: %v", err)
	}
	gotZero, err := r.Serialise(zero)
	if err != nil {
		t.Fatalf("Serialise zero-origin image returned error: %v", err)
	}
	if !reflect.DeepEqual(gotShifted, gotZero) {
		t.Fatalf("Serialise shifted = [% x], want zero-origin [% x]", gotShifted, gotZero)
	}
}

func TestGenericRasteriserSerialiseRightSidePadding(t *testing.T) {
	r := testRasteriser(16, 2)
	img := strictImage{Gray: image.NewGray(image.Rect(0, 0, 8, 1))}
	for x := range 8 {
		img.Set(x, 0, image.Black)
	}

	got, err := r.Serialise(img)
	if err != nil {
		t.Fatalf("Serialise returned error: %v", err)
	}
	want := []byte{0x00, 0xff, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(got[0], want) {
		t.Fatalf("Serialise packet = [% x], want [% x]", got[0], want)
	}
}

func testRasteriser(width, linesPerPacket int) *GenericRasteriser {
	return &GenericRasteriser{
		Width:          width,
		Dpi:            203,
		LinesPerPacket: linesPerPacket,
		PrefixFunc: func(packetIndex int) []byte {
			return []byte{byte(packetIndex)}
		},
		Terminator: 0x00,
		Threshold:  bitmap.DefaultThreshold,
	}
}

type strictImage struct {
	*image.Gray
}

func (s strictImage) At(x, y int) color.Color {
	if !image.Pt(x, y).In(s.Bounds()) {
		panic("At called outside image bounds")
	}
	return s.Gray.At(x, y)
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

func makeTextLikePattern(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, image.White)
			if y%6 == 1 || (x%11 < 2 && y%6 < 5) {
				img.Set(x, y, image.Black)
			}
		}
	}
	return img
}

func assertRasterPackets(t *testing.T, got [][]byte, r *GenericRasteriser) {
	t.Helper()
	if len(got) == 0 {
		t.Fatal("Serialise returned no packets")
	}
	packetLen := len(r.PrefixFunc(0)) + r.Width/8*r.LinesPerPacket + 1
	nonEmpty := false
	for i, packet := range got {
		if len(packet) != packetLen {
			t.Fatalf("packet %d length = %d, want %d", i, len(packet), packetLen)
		}
		if packet[len(packet)-1] != r.Terminator {
			t.Fatalf("packet %d terminator = %#x, want %#x", i, packet[len(packet)-1], r.Terminator)
		}
		for _, b := range packet[len(r.PrefixFunc(0)) : len(packet)-1] {
			if b != 0 {
				nonEmpty = true
				break
			}
		}
	}
	if !nonEmpty {
		t.Fatal("Serialise returned only empty raster data")
	}
}
