package cupsraster

import (
	"bytes"
	"encoding/binary"
	"image"
	"strings"
	"testing"
)

// buildPWGHeader assembles a 1796-byte page header with the fields the
// decoder reads.
func buildPWGHeader(width, height, bitsPerPixel, colorSpace int) []byte {
	hdr := make([]byte, pwgHeaderSize)
	copy(hdr, pwgMagic)
	u32 := func(off, v int) { binary.BigEndian.PutUint32(hdr[off:off+4], uint32(v)) }
	u32(pwgOffHWResolutionX, 203)
	u32(pwgOffHWResolutionY, 203)
	u32(pwgOffWidth, width)
	u32(pwgOffHeight, height)
	bpc := bitsPerPixel
	if bitsPerPixel == 24 {
		bpc = 8
	}
	u32(pwgOffBitsPerColor, bpc)
	u32(pwgOffBitsPerPixel, bitsPerPixel)
	u32(pwgOffBytesPerLine, (width*bitsPerPixel+7)/8)
	u32(pwgOffColorOrder, 0)
	u32(pwgOffColorSpace, colorSpace)
	return hdr
}

// buildPWGStream assembles a full PWG stream of pages, each given as rows.
func buildPWGStream(t *testing.T, hdrRows ...struct {
	hdr  []byte
	rows [][]byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString(pwgSyncWord)
	for _, p := range hdrRows {
		buf.Write(p.hdr)
		groupSize := 1
		if bpp := int(binary.BigEndian.Uint32(p.hdr[pwgOffBitsPerPixel:])); bpp == 24 {
			groupSize = 3
		}
		encodePage(&buf, p.rows, groupSize)
	}
	return buf.Bytes()
}

type pwgPage = struct {
	hdr  []byte
	rows [][]byte
}

func TestDecodePWG_Black1(t *testing.T) {
	// 16x2, K space, 1 bit: set bit means BLACK.
	rows := [][]byte{
		{0xff, 0x00}, // 8 black, 8 white
		{0x80, 0x01}, // black at x=0 and x=15
	}
	stream := buildPWGStream(t, pwgPage{buildPWGHeader(16, 2, 1, pwgCSBlack), rows})

	pages, err := DecodePWG(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	img := pages[0].(*image.Gray)
	if got := img.Bounds(); got != image.Rect(0, 0, 16, 2) {
		t.Fatalf("bounds %v", got)
	}
	// K polarity: bit 1 = black (gray 0)
	for x := range 8 {
		if img.GrayAt(x, 0).Y != 0 {
			t.Errorf("pixel (%d,0): got %d, want black (0)", x, img.GrayAt(x, 0).Y)
		}
	}
	for x := 8; x < 16; x++ {
		if img.GrayAt(x, 0).Y != 0xff {
			t.Errorf("pixel (%d,0): got %d, want white (255)", x, img.GrayAt(x, 0).Y)
		}
	}
	if img.GrayAt(0, 1).Y != 0 || img.GrayAt(15, 1).Y != 0 {
		t.Error("edge pixels on row 1 must be black")
	}
	if img.GrayAt(7, 1).Y != 0xff {
		t.Error("middle pixel on row 1 must be white")
	}
}

func TestDecodePWG_SGray1Polarity(t *testing.T) {
	// Same bit pattern as K test but sGray: bit 0 = BLACK (inverse).
	rows := [][]byte{{0xff, 0x00}}
	stream := buildPWGStream(t, pwgPage{buildPWGHeader(16, 1, 1, pwgCSSGray), rows})
	pages, err := DecodePWG(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	img := pages[0].(*image.Gray)
	if img.GrayAt(0, 0).Y != 0xff {
		t.Error("sGray bit 1 must decode white")
	}
	if img.GrayAt(15, 0).Y != 0x00 {
		t.Error("sGray bit 0 must decode black")
	}
}

func TestDecodePWG_SGray8(t *testing.T) {
	rows := [][]byte{{0, 64, 128, 255}}
	stream := buildPWGStream(t, pwgPage{buildPWGHeader(4, 1, 8, pwgCSSGray), rows})
	pages, err := DecodePWG(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	img := pages[0].(*image.Gray)
	for x, want := range []byte{0, 64, 128, 255} {
		if got := img.GrayAt(x, 0).Y; got != want {
			t.Errorf("pixel %d: got %d, want %d", x, got, want)
		}
	}
}

func TestDecodePWG_SRGB24(t *testing.T) {
	rows := [][]byte{{255, 0, 0, 0, 255, 0, 0, 0, 255}}
	stream := buildPWGStream(t, pwgPage{buildPWGHeader(3, 1, 24, pwgCSSRGB), rows})
	pages, err := DecodePWG(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	img := pages[0].(*image.NRGBA)
	if c := img.NRGBAAt(0, 0); c.R != 255 || c.G != 0 || c.B != 0 {
		t.Errorf("pixel 0: %v, want red", c)
	}
	if c := img.NRGBAAt(2, 0); c.B != 255 {
		t.Errorf("pixel 2: %v, want blue", c)
	}
}

func TestDecodePWG_MultiPage(t *testing.T) {
	p1 := pwgPage{buildPWGHeader(8, 2, 1, pwgCSBlack), [][]byte{{0xff}, {0xff}}}
	p2 := pwgPage{buildPWGHeader(8, 1, 1, pwgCSBlack), [][]byte{{0x00}}}
	stream := buildPWGStream(t, p1, p2)
	pages, err := DecodePWG(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if pages[0].Bounds().Dy() != 2 || pages[1].Bounds().Dy() != 1 {
		t.Error("page dimensions mixed up")
	}
}

func TestDecodePWG_Errors(t *testing.T) {
	valid := pwgPage{buildPWGHeader(8, 1, 1, pwgCSBlack), [][]byte{{0x00}}}
	tests := []struct {
		name    string
		stream  []byte
		wantSub string
	}{
		{"bad sync", []byte("XXXX"), "sync word"},
		{"bad magic", append([]byte(pwgSyncWord), make([]byte, pwgHeaderSize)...), "header does not start"},
		{"truncated header", []byte(pwgSyncWord + pwgMagic), "reading header"},
		{"truncated page data", buildPWGStream(t, valid)[:len(pwgSyncWord)+pwgHeaderSize+1], "page 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodePWG(bytes.NewReader(tt.stream))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestParsePWGHeader_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mangle func(hdr []byte)
	}{
		{"zero width", func(h []byte) { binary.BigEndian.PutUint32(h[pwgOffWidth:], 0) }},
		{"huge height", func(h []byte) { binary.BigEndian.PutUint32(h[pwgOffHeight:], 1e6) }},
		{"banded color order", func(h []byte) { binary.BigEndian.PutUint32(h[pwgOffColorOrder:], 1) }},
		{"unknown color space", func(h []byte) { binary.BigEndian.PutUint32(h[pwgOffColorSpace:], 99) }},
		{"bpl mismatch", func(h []byte) { binary.BigEndian.PutUint32(h[pwgOffBytesPerLine:], 7) }},
		{"bpp/cspace mismatch", func(h []byte) { binary.BigEndian.PutUint32(h[pwgOffBitsPerPixel:], 24) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := buildPWGHeader(16, 4, 1, pwgCSBlack)
			tt.mangle(hdr)
			if _, err := parsePWGHeader(hdr); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestDetect(t *testing.T) {
	pwg := buildPWGStream(t, pwgPage{buildPWGHeader(8, 1, 1, pwgCSBlack), [][]byte{{0x00}}})
	tests := []struct {
		name string
		data []byte
		want Format
	}{
		{"pwg", pwg, FormatPWG},
		{"urf", []byte("UNIRAST\x00\x00\x00\x00\x01"), FormatURF},
		{"cups raster LE lookalike", []byte("RaS2" + "NotPwgRas\x00" + "xxxx"), FormatUnknown},
		{"pdf", []byte("%PDF-1.7 ......."), FormatUnknown},
		{"png", []byte("\x89PNG\r\n\x1a\n........"), FormatUnknown},
		{"short", []byte("RaS2"), FormatUnknown},
		{"empty", nil, FormatUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(tt.data); got != tt.want {
				t.Errorf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}
