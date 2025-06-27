package thermoprint

import (
	"bufio"
	"image"
	"image/draw"
	"io"

	"golang.org/x/image/font"
)

// Composer is a struct that allows appending images to a destination image.
type Composer struct {
	dst *image.RGBA // destination image (canvas)
	sp  image.Point // current image position

	crop       bool
	ditherFunc DitherFunc // optional dithering function
	ditherText bool       // whether to dither text or not
}

type ComposerOption func(*Composer)

// WithComposerCrop sets the crop option for the Composer.
func WithComposerCrop(crop bool) ComposerOption {
	return func(c *Composer) {
		c.crop = crop
	}
}

// WithComposerDitherFunc sets the dithering function for the Composer.
func WithComposerDitherFunc(dfn DitherFunc) ComposerOption {
	return func(c *Composer) {
		c.ditherFunc = dfn
	}
}

func WithComposerDitherText(ditherText bool) ComposerOption {
	return func(c *Composer) {
		c.ditherText = ditherText
	}
}

func NewComposer(width int, opt ...ComposerOption) *Composer {
	img := image.NewRGBA(image.Rect(0, 0, width, 1))
	return &Composer{
		dst: img,
		sp:  image.Point{},
	}
}

// AppendImage appends an image without dithering.
func (c *Composer) AppendImage(img image.Image) {
	c.appendImageDither(img, c.ditherFunc)
}

func (c *Composer) appendImageDither(img image.Image, dfn DitherFunc) {
	// c.sp contains the current position in the destination image
	// we need to check if the img fits the c.dst at the current position
	// and if not, we need to resize the destination image
	if img == nil {
		return // nothing to append
	}
	// check if the new image size is larger than the destination image
	if c.dst.Bounds().Dx() < img.Bounds().Dx() && !c.crop {
		img = resize(img, c.dst.Bounds().Dx())
	}
	// check if the current position + image height exceeds the destination
	// image height
	if c.sp.Y+img.Bounds().Dy() > c.dst.Bounds().Dy() {
		c.dst = resizeY(c.dst, c.sp.Y+img.Bounds().Dy())
	}
	// update the current position in the destination image
	// draw the image at the current position
	if dfn != nil {
		img = dfn(img, 0.0) // apply dithering function if provided
	} else {
		// default to no dithering
		img = DitherThresholdFn(DefaultThreshold)(img, 0.0)
	}
	draw.Draw(c.dst, img.Bounds(), img, c.sp, draw.Over)
	c.sp.Y += img.Bounds().Dy() // move down by the height of the new image
	c.sp.X = 0                  // reset X position to the start of the line
}

func (c *Composer) AppendText(face font.Face, text string) error {
	img, err := renderTTF(text, face, c.dst.Bounds().Dx())
	if err != nil {
		return err
	}
	if c.ditherText {
		c.appendImageDither(img, c.ditherFunc)
	} else {
		c.appendImageDither(img, nil) // no dithering for text
	}
	return nil
}

// Image returns the composed image.
func (c *Composer) Image() image.Image {
	return c.dst
}

var commands = map[string]any{
	".image":  true, // embed image
	".font":   true, // set font
	".center": true, // center following lines
	".left":   true, // left align following lines
	".right":  true, // right align following lines
}

// ParseComposeScript will parse a script from the reader and return a composed
// image. The script can contain commands like ".image", ".font", ".center",
// ".left", ".right". It will read the script line by line, execute the
// commands, and return the final image.
func (c *Composer) ParseComposeScript(r io.Reader) error {
	s := bufio.NewScanner(r)
	for s.Scan() {

	}
	if err := s.Err(); err != nil {
		return err
	}

}
