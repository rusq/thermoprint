package cupsraster

import (
	"bytes"
	"encoding/binary"
	"image"
	"strings"
	"testing"
)

func buildURFPageHeader(width, height, bitsPerPixel, colorSpace int) []byte {
	hdr := make([]byte, urfPageHeaderSize)
	hdr[0] = byte(bitsPerPixel)
	hdr[1] = byte(colorSpace)
	binary.BigEndian.PutUint32(hdr[12:], uint32(width))
	binary.BigEndian.PutUint32(hdr[16:], uint32(height))
	binary.BigEndian.PutUint32(hdr[20:], 203)
	return hdr
}

type urfPage = struct {
	hdr  []byte
	rows [][]byte
}

func buildURFStream(t *testing.T, pages ...urfPage) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString(urfMagic)
	binary.Write(&buf, binary.BigEndian, uint32(len(pages)))
	for _, p := range pages {
		buf.Write(p.hdr)
		groupSize := 1
		if int(p.hdr[0]) == 24 {
			groupSize = 3
		}
		encodePage(&buf, p.rows, groupSize)
	}
	return buf.Bytes()
}

func TestDecodeURF_Mono1Polarity(t *testing.T) {
	// 1-bit URF uses ink semantics (bit 1 = BLACK), matching PWG's K space
	// — verified against real macOS output in fixture_test.go.
	rows := [][]byte{{0xff, 0x00}} // 8 black, 8 white
	stream := buildURFStream(t, urfPage{buildURFPageHeader(16, 1, 1, urfCSSGray), rows})
	pages, err := DecodeURF(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	img := pages[0].(*image.Gray)
	if img.GrayAt(0, 0).Y != 0x00 {
		t.Error("URF bit 1 must decode black")
	}
	if img.GrayAt(15, 0).Y != 0xff {
		t.Error("URF bit 0 must decode white")
	}
}

func TestDecodeURF_Gray8(t *testing.T) {
	rows := [][]byte{{0, 128, 255, 32}, {1, 2, 3, 4}}
	stream := buildURFStream(t, urfPage{buildURFPageHeader(4, 2, 8, urfCSSGray), rows})
	pages, err := DecodeURF(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	img := pages[0].(*image.Gray)
	if img.GrayAt(0, 0).Y != 0 || img.GrayAt(2, 0).Y != 255 || img.GrayAt(3, 1).Y != 4 {
		t.Error("gray values must pass through unchanged")
	}
}

func TestDecodeURF_RGB24(t *testing.T) {
	rows := [][]byte{{10, 20, 30, 40, 50, 60}}
	stream := buildURFStream(t, urfPage{buildURFPageHeader(2, 1, 24, urfCSSRGB), rows})
	pages, err := DecodeURF(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	img := pages[0].(*image.NRGBA)
	if c := img.NRGBAAt(1, 0); c.R != 40 || c.G != 50 || c.B != 60 {
		t.Errorf("pixel 1: %v", c)
	}
}

func TestDecodeURF_MultiPage(t *testing.T) {
	p1 := urfPage{buildURFPageHeader(8, 1, 1, urfCSSGray), [][]byte{{0xff}}}
	p2 := urfPage{buildURFPageHeader(8, 2, 8, urfCSSGray), [][]byte{{1, 2, 3, 4, 5, 6, 7, 8}, {9, 10, 11, 12, 13, 14, 15, 16}}}
	pages, err := DecodeURF(bytes.NewReader(buildURFStream(t, p1, p2)))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
}

func TestDecodeURF_Errors(t *testing.T) {
	valid := buildURFStream(t, urfPage{buildURFPageHeader(8, 1, 1, urfCSSGray), [][]byte{{0x00}}})
	badCount := append([]byte(urfMagic), 0, 0, 0, 0)
	tests := []struct {
		name    string
		stream  []byte
		wantSub string
	}{
		{"bad magic", []byte("NOTURAST\x00\x00\x00\x01"), "not a URF stream"},
		{"zero pages", badCount, "page count"},
		{"truncated page header", valid[:len(urfMagic)+4+10], "page 1"},
		{"truncated page data", valid[:len(valid)-1], "page 1"},
		{"bad colorspace", buildURFStream(t, urfPage{buildURFPageHeader(8, 1, 1, 7), [][]byte{{0x00}}}), "color space"},
		{"bad bpp", buildURFStream(t, urfPage{buildURFPageHeader(8, 1, 4, urfCSSGray), [][]byte{{0x00}}}), "bits per pixel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeURF(bytes.NewReader(tt.stream))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestDecode_Dispatch(t *testing.T) {
	pwg := buildPWGStream(t, pwgPage{buildPWGHeader(8, 1, 1, pwgCSBlack), [][]byte{{0x00}}})
	urf := buildURFStream(t, urfPage{buildURFPageHeader(8, 1, 1, urfCSSGray), [][]byte{{0xff}}})

	if pages, err := Decode(bytes.NewReader(pwg)); err != nil || len(pages) != 1 {
		t.Errorf("Decode(pwg): pages=%d err=%v", len(pages), err)
	}
	if pages, err := Decode(bytes.NewReader(urf)); err != nil || len(pages) != 1 {
		t.Errorf("Decode(urf): pages=%d err=%v", len(pages), err)
	}
	if _, err := Decode(bytes.NewReader([]byte("%PDF-1.7 not raster data"))); err == nil {
		t.Error("Decode(pdf) must fail")
	}
}
