package fontmgr

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const testBDF = `STARTFONT 2.1
FONT test
SIZE 8 72 72
FONTBOUNDINGBOX 9 8 $ 0 -2
STARTPROPERTIES 3
FONT_ASCENT 6
FONT_DESCENT 2
DEFAULT_CHAR 65
ENDPROPERTIES
CHARS 2
STARTCHAR A
ENCODING 65
DWIDTH 6 0
BBX 5 7 0 -1
BITMAP
70
88
88
F8
88
88
88
ENDCHAR
STARTCHAR CYRILLIC_A
ENCODING 1040
DWIDTH 10 0
BBX 9 8 0 -2
BITMAP
1F00
2080
2080
3F80
2080
2080
2080
2080
ENDCHAR
ENDFONT
`

func TestLoadFromFile_BDF(t *testing.T) {
	for _, ext := range []string{".bdf", ".BDF"} {
		t.Run(ext, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "font"+ext)
			if err := os.WriteFile(filename, []byte(testBDF), 0o600); err != nil {
				t.Fatalf("write BDF fixture: %v", err)
			}

			face, err := LoadFromFile(filename, 72, 203)
			if err != nil {
				t.Fatalf("LoadFromFile(%q) error = %v", filename, err)
			}
			t.Cleanup(func() { _ = face.Close() })
			if got, want := face.Metrics().Height, fixed.I(8); got != want {
				t.Errorf("face height = %v, want %v", got, want)
			}
			if got, ok := face.GlyphAdvance('А'); !ok || got != fixed.I(10) {
				t.Errorf("Cyrillic glyph advance = %v, %v; want %v, true", got, ok, fixed.I(10))
			}

			img := image.NewRGBA(image.Rect(0, 0, 32, face.Metrics().Height.Ceil()))
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := 0; x < img.Bounds().Dx(); x++ {
					img.Set(x, y, color.White)
				}
			}
			d := font.Drawer{
				Dst:  img,
				Src:  image.Black,
				Face: face,
				Dot:  fixed.P(0, face.Metrics().Ascent.Ceil()),
			}
			d.DrawString("AА")
			if !containsBlack(img) {
				t.Error("rendered BDF text contains no black pixels")
			}
		})
	}
}

func TestLoadFromFile_BDFErrors(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "bad.bdf")
		if err := os.WriteFile(filename, []byte("STARTFONT 2.1\nCHARS 1\n"), 0o600); err != nil {
			t.Fatalf("write malformed BDF: %v", err)
		}
		if _, err := LoadFromFile(filename, 0, 0); err == nil {
			t.Fatal("LoadFromFile() error = nil, want malformed BDF error")
		}
	})

	t.Run("oversized", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "large.bdf")
		f, err := os.Create(filename)
		if err != nil {
			t.Fatalf("create oversized BDF: %v", err)
		}
		if err := f.Truncate(maxExternalFontSize + 1); err != nil {
			_ = f.Close()
			t.Fatalf("truncate oversized BDF: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close oversized BDF: %v", err)
		}
		_, err = LoadFromFile(filename, 0, 0)
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("LoadFromFile() error = %v, want too-large error", err)
		}
	})

	t.Run("unsupported extension", func(t *testing.T) {
		if _, err := LoadFromFile("font.pcf", 0, 0); err == nil || !strings.Contains(err.Error(), "unsupported font type") {
			t.Fatalf("LoadFromFile() error = %v, want unsupported-type error", err)
		}
	})
}

func containsBlack(img image.Image) bool {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r == 0 && g == 0 && b == 0 {
				return true
			}
		}
	}
	return false
}
