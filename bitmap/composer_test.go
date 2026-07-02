package bitmap

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/rusq/thermoprint/fontmgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewComposer_appliesOptions(t *testing.T) {
	t.Run("crop", func(t *testing.T) {
		c := NewComposer(2, WithComposerCrop(true))
		c.AppendImage(testColorImage(image.Rect(0, 0, 4, 4), color.White))

		assert.Equal(t, image.Rect(0, 0, 2, 4), c.Bounds())
	})

	t.Run("image dither", func(t *testing.T) {
		var calls int
		dfn := func(img image.Image, gamma float64) image.Image {
			calls++
			return img
		}
		c := NewComposer(2, WithComposerDitherFunc(dfn))

		c.AppendImage(testColorImage(image.Rect(0, 0, 2, 2), color.White))

		assert.Equal(t, 1, calls)
	})

	t.Run("text dither", func(t *testing.T) {
		var calls int
		dfn := func(img image.Image, gamma float64) image.Image {
			calls++
			return img
		}
		c := NewComposer(
			64,
			WithComposerDitherFunc(dfn),
			WithComposerEnableTextDither(true),
		)

		err := c.AppendText(fontmgr.DefaultFont, "hello")

		require.NoError(t, err)
		assert.Equal(t, 1, calls)
	})
}

func TestDocument_ParseImageCommandRequiresArgument(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{
			name:   "long command",
			script: ".image\n",
		},
		{
			name:   "short command",
			script: ".im\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := NewDocument(NewComposer(2), 203)

			err := doc.Parse(strings.NewReader(tt.script))

			require.Error(t, err)
			assert.ErrorContains(t, err, "line 1")
			assert.ErrorContains(t, err, "invalid argument count, expected 1, provided: 0")
		})
	}
}

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
			c.AppendImageDither(tt.args.img, tt.args.dfn)
			assert.Equal(t, tt.wantImage, c.dst)
		})
	}
}
