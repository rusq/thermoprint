package bitmap

import (
	"image"
	"image/color"
	"image/draw"
	"reflect"
	"testing"
)

func fillColor(dst *image.RGBA, rect image.Rectangle, col color.Color) *image.RGBA {
	r := rect
	sp := rect.Min
	draw.Draw(dst, r, image.NewUniform(col), sp, draw.Src)
	return dst
}

func testColorImage(rect image.Rectangle, col color.Color) *image.RGBA {
	m := image.NewRGBA(rect)
	fillColor(m, m.Bounds(), col)
	return m
}

func TestResizeCanvasY(t *testing.T) {
	type args struct {
		dst       *image.RGBA
		newHeight int
	}
	tests := []struct {
		name string
		args args
		want *image.RGBA
	}{
		{
			name: "resizes canvas",
			args: args{
				dst:       image.NewRGBA(image.Rect(0, 0, 2, 1)),
				newHeight: 2,
			},
			want: fillColor(image.NewRGBA(image.Rect(0, 0, 2, 2)), image.Rect(0, 1, 2, 2), color.White),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResizeCanvasY(tt.args.dst, tt.args.newHeight); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResizeCanvasY() = %v, want %v", got, tt.want)
			}
		})
	}
}
