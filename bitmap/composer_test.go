package bitmap

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposer_appendImageDither(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	fillColor(src, src.Bounds(), color.White)
	type fields struct {
		dst        *image.RGBA
		sp         image.Point
		crop       bool
		ditherFunc DitherFunc
		ditherText bool
	}
	type args struct {
		img image.Image
		dfn DitherFunc
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		wantImage image.Image
	}{
		{
			name: "appends image to initial image",
			fields: fields{
				dst: image.NewRGBA(image.Rect(0, 0, 2, 0)),
				sp:  image.Point{},
			},
			args: args{
				img: src,
				dfn: nil,
			},
			wantImage: src,
		},
		{
			name: "appends image to existing one",
			fields: fields{
				dst: testColorImage(src.Bounds(), color.White),
				sp:  image.Point{0, src.Bounds().Dy()},
			},
			args: args{
				img: testColorImage(src.Bounds(), color.Black),
				dfn: nil,
			},
			wantImage: &image.RGBA{
				Pix: []uint8{
					0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
					0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
					0x0, 0x0, 0x0, 0xff, 0x0, 0x0, 0x0, 0xff,
					0x0, 0x0, 0x0, 0xff, 0x0, 0x0, 0x0, 0xff,
				},
				Stride: 8,
				Rect: image.Rectangle{
					Min: image.Point{X: 0, Y: 0},
					Max: image.Point{X: 2, Y: 4},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Composer{
				dst:        tt.fields.dst,
				sp:         tt.fields.sp,
				crop:       tt.fields.crop,
				ditherFunc: tt.fields.ditherFunc,
				ditherText: tt.fields.ditherText,
			}
			c.appendImageDither(tt.args.img, tt.args.dfn)
			assert.Equal(t, tt.wantImage, c.dst)
		})
	}
}
