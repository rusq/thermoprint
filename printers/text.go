package printers

import (
	"image"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

var (
	replacer = strings.NewReplacer("\t", strings.Repeat(" ", 8))
)

func renderTTF(text string, face font.Face, imgWidth int) (image.Image, error) {
	lines := strings.Split(text, "\n")
	imgHeight := len(lines) * face.Metrics().Height.Ceil()
	// lineHeight := face.Metrics().Ascent.Ceil() + face.Metrics().Height.Ceil()

	fg, bg := image.Black, image.White
	img := image.NewRGBA(image.Rect(0, 0, imgWidth, imgHeight))
	draw.Draw(img, img.Bounds(), bg, image.Point{}, draw.Src)

	var d = font.Drawer{
		Dst:  img,
		Src:  fg,
		Face: face,
		Dot:  fixed.P(0, face.Metrics().Ascent.Ceil()), // Start at the top
	}
	for _, line := range lines {
		d.DrawString(replacer.Replace(line))
		d.Dot.X = fixed.I(0) // Reset X position to the start of the line
		d.Dot.Y += face.Metrics().Height
	}
	return img, nil
}
