package cupsraster

import (
	"bytes"
	"image"
	"os"
	"testing"
)

// The testdata fixtures were generated on macOS from the same source image
// (black border + black bars on white, 384x320) with:
//
//	cupsfilter -i image/png -m image/pwg-raster -p ippsrv/ppd/LX-D02.ppd -o Resolution=203dpi fixture.png > doc.pwg
//	cupsfilter -i image/png -m image/urf        -p ippsrv/ppd/LX-D02.ppd -o Resolution=203dpi fixture.png > doc.urf
//
// Decoding both must yield the same page with the same polarity: black
// content black, white background white — PWG black_1 (K space, bit 1 =
// black) and URF mono (sGray, bit 0 = black) use opposite bit semantics for
// identical content.

func loadFixture(t *testing.T, name string, want Format) image.Image {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if got := Detect(data); got != want {
		t.Fatalf("Detect(%s) = %v, want %v", name, got, want)
	}
	pages, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode(%s): %v", name, err)
	}
	if len(pages) != 1 {
		t.Fatalf("%s: got %d pages, want 1", name, len(pages))
	}
	return pages[0]
}

// blackFraction returns the fraction of pixels darker than mid-gray.
func blackFraction(img image.Image) float64 {
	b := img.Bounds()
	var black, total int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			if (r+g+bb)/3 < 0x8000 {
				black++
			}
			total++
		}
	}
	return float64(black) / float64(total)
}

func TestFixtures_PolarityAndConsistency(t *testing.T) {
	pwg := loadFixture(t, "doc.pwg", FormatPWG)
	urf := loadFixture(t, "doc.urf", FormatURF)

	// the macOS cupsfilter fixtures are 100dpi (it ignores -o Resolution);
	// the declared resolution must be surfaced so the printer can rescale.
	for _, name := range []string{"doc.pwg", "doc.urf"} {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		pages, err := DecodePages(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if pages[0].XDPI != 100 || pages[0].YDPI != 100 {
			t.Errorf("%s: declared resolution %dx%d, want 100x100", name, pages[0].XDPI, pages[0].YDPI)
		}
	}

	if pwg.Bounds() != urf.Bounds() {
		t.Fatalf("bounds differ: pwg %v, urf %v", pwg.Bounds(), urf.Bounds())
	}
	t.Logf("fixture page: %v", pwg.Bounds())

	for _, tt := range []struct {
		name string
		img  image.Image
	}{{"pwg", pwg}, {"urf", urf}} {
		frac := blackFraction(tt.img)
		t.Logf("%s black fraction: %.3f", tt.name, frac)
		if frac < 0.02 {
			t.Errorf("%s: black fraction %.3f — page looks blank; polarity inverted or content lost", tt.name, frac)
		}
		if frac > 0.5 {
			t.Errorf("%s: black fraction %.3f — page mostly black; polarity likely inverted", tt.name, frac)
		}
	}

	// Both formats must agree pixel-perfectly on 1-bit content rendered
	// from the same source through the same PPD.
	b := pwg.Bounds()
	diff := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			pr, _, _, _ := pwg.At(x, y).RGBA()
			ur, _, _, _ := urf.At(x, y).RGBA()
			if (pr < 0x8000) != (ur < 0x8000) {
				diff++
			}
		}
	}
	total := b.Dx() * b.Dy()
	if frac := float64(diff) / float64(total); frac > 0.01 {
		t.Errorf("pwg and urf pages disagree on %.2f%% of pixels — polarity mismatch between decoders", frac*100)
	}
}
