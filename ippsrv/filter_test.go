package ippsrv

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingFilter records whether the fallback was invoked.
type recordingFilter struct {
	called bool
	data   []byte
}

func (f *recordingFilter) ToRaster(_ context.Context, _ int, data []byte) ([]image.Image, error) {
	f.called = true
	f.data = data
	return []image.Image{image.NewGray(image.Rect(0, 0, 1, 1))}, nil
}

func (f *recordingFilter) Type() string { return "recording" }

// minimalURF builds the smallest valid URF stream: one 8x1 white 1-bit page.
func minimalURF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("UNIRAST\x00")
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(1)))
	hdr := make([]byte, 32)
	hdr[0] = 1                                // bpp
	hdr[1] = 0                                // sGray
	binary.BigEndian.PutUint32(hdr[12:], 8)   // width
	binary.BigEndian.PutUint32(hdr[16:], 1)   // height
	binary.BigEndian.PutUint32(hdr[20:], 203) // dpi
	buf.Write(hdr)
	buf.Write([]byte{0x00 /* lineRepeat */, 0x00 /* 1 group */, 0x00 /* white */})
	return buf.Bytes()
}

func TestRasterSniffFilter_Routing(t *testing.T) {
	pwg, err := os.ReadFile("../cupsraster/testdata/doc.pwg")
	require.NoError(t, err)

	tests := []struct {
		name         string
		data         []byte
		wantFallback bool
		wantPages    int
	}{
		{"pwg routed to decoder", pwg, false, 1},
		{"urf routed to decoder", minimalURF(t), false, 1},
		{"pdf falls back", []byte("%PDF-1.7 not raster"), true, 1},
		{"garbage falls back", []byte("hello world, definitely not raster"), true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordingFilter{}
			f := &rasterSniffFilter{fallback: rec}
			pages, err := f.ToRaster(context.Background(), 203, tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFallback, rec.called, "fallback invocation")
			assert.Len(t, pages, tt.wantPages)
			if tt.wantFallback {
				assert.Equal(t, tt.data, rec.data, "fallback must receive the original data")
			}
		})
	}
}

func TestRasterSniffFilter_Type(t *testing.T) {
	f := &rasterSniffFilter{fallback: &imageMagickFilter{}}
	assert.Equal(t, "raster+ImageMagick", f.Type())
}
