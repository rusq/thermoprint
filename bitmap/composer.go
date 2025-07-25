package bitmap

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/font"

	"github.com/rusq/thermoprint/fontmgr"
)

// Composer is an abstraction that allows composing the document from text and
// images.
type Composer struct {
	dst *image.RGBA // destination image (canvas)
	sp  image.Point // current image position

	crop       bool
	ditherFunc DitherFunc // optional dithering function
	ditherText bool       // whether to dither text or not
}

// ComposerOption is a functional option for the [Composer].
type ComposerOption func(*Composer)

// WithComposerCrop sets the crop parameter to the given value.  If crop is
// enabled, and an image wider than the canvas width is being appended, it will
// be cropped on the right margin, instead of rescaled.
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

// WithComposerEnableTextDither enables dithering of text.
func WithComposerEnableTextDither(ditherText bool) ComposerOption {
	return func(c *Composer) {
		c.ditherText = ditherText
	}
}

// NewComposer initialises a new composer with a given canvas width.
func NewComposer(width int, opt ...ComposerOption) *Composer {
	img := image.NewRGBA(image.Rect(0, 0, width, 1))
	return &Composer{
		dst: img,
		sp:  image.Point{},
	}
}

// AppendImage appends an image without dithering to the bottom of the canvas.
func (c *Composer) AppendImage(img image.Image) {
	c.AppendImageDither(img, c.ditherFunc)
}

// AppendImageDither appends the image with a custom dither function at the
// bottom of the image canvas.
func (c *Composer) AppendImageDither(img image.Image, dfn DitherFunc) {
	// c.sp contains the current position in the destination image
	// we need to check if the img fits the c.dst at the current position
	// and if not, we need to resize the destination image
	if img == nil {
		return // nothing to append
	}
	// check if the new image size is larger than the destination image
	if c.dst.Bounds().Dx() < img.Bounds().Dx() && !c.crop {
		img = ResizeToFit(img, c.dst.Bounds().Dx())
	}
	// check if the current position + image height exceeds the destination
	// image height
	if c.sp.Y+img.Bounds().Dy() > c.dst.Bounds().Dy() {
		c.dst = ResizeCanvasY(c.dst, c.sp.Y+img.Bounds().Dy())
	}
	// update the current position in the destination image
	// draw the image at the current position
	if dfn != nil {
		img = dfn(img, DefaultGamma) // apply dithering function if provided
	} else {
		// default to no dithering
		img = DitherThresholdFn(DefaultThreshold)(img, DefaultGamma)
	}
	draw.Draw(c.dst, img.Bounds().Add(c.sp), img, image.Point{}, draw.Src)
	c.sp.Y += img.Bounds().Dy() // move down by the height of the new image
	c.sp.X = 0                  // reset X position to the start of the line
}

// AppendText renders text at the bottom of the image, growing the underlying
// image canvas if needed to fit the text lines.
func (c *Composer) AppendText(face font.Face, text string) error {
	img, err := RenderTTF(text, face, c.dst.Bounds().Dx())
	if err != nil {
		return err
	}
	if c.ditherText {
		c.AppendImageDither(img, c.ditherFunc)
	} else {
		c.AppendImageDither(img, nil) // no dithering for text
	}
	return nil
}

// Image returns the composed image.
func (c *Composer) Image() image.Image {
	return c.dst
}

// Bounds returns the current canvas rectangle.
func (c *Composer) Bounds() image.Rectangle {
	return c.dst.Bounds()
}

type documentCommand string

const (
	dcImage  = ".image"
	dcImageS = ".im"
	dcFont   = ".font"
	dcFontS  = ".ft"
	dcAlign  = ".align"
	dcAlignS = ".al"
)

var commands = map[string]func(doc *Document, args ...string) error{
	dcImage:  (*Document).cmdImage, // embed image
	dcImageS: (*Document).cmdImage, // embed image
	dcFont:   (*Document).cmdFont,  // set font
	dcFontS:  (*Document).cmdFont,  // set font
	dcAlign:  (*Document).cmdAlign, // align text
	dcAlignS: (*Document).cmdAlign, // align text
}

type textAlign int

const (
	alignLeft textAlign = iota
	alignCenter
	alignRight
)

// Document is an abstraction that allows to manipulate composer with simple
// text scripts.
type Document struct {
	c         *Composer
	dpi       float64
	width     int
	alignment textAlign // current text alignment
	font      font.Face // selected font
	buf       bytes.Buffer
}

// NewDocument creates a new document over the composer.
func NewDocument(c *Composer, dpi float64) *Document {
	return &Document{
		c:         c,
		dpi:       dpi,
		width:     c.Bounds().Dx(),
		alignment: alignLeft,
		font:      fontmgr.DefaultFont,
	}
}

// WriteString adds a line of text to the buffer with the current alignment.
func (d *Document) WriteString(s string) (n int, err error) {
	// TODO: alignment
	return d.buf.WriteString(s)
}

// flush flushes text onto composer.
func (d *Document) flush() {
	if d.buf.Len() == 0 {
		return
	}
	d.c.AppendText(d.font, d.buf.String())
	d.buf.Reset()
}

// Parse reads the script from reader and processes commands.
func (d *Document) Parse(r io.Reader) error {
	s := bufio.NewScanner(r)
	for n := 1; s.Scan(); n++ {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			continue // skip empty lines
		}
		if text[0] == '.' {
			if err := d.parseCommand(text); err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
			continue
		}
		if _, err := d.WriteString(text + "\n"); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	d.flush() // flush any remaining text in the buffer
	return nil
}

func (d *Document) parseCommand(text string) error {
	// process command
	d.flush()
	parts := strings.Split(text, " ")
	fn, ok := commands[parts[0]]
	if !ok {
		return fmt.Errorf("unknown or disabled command %q", parts[0])
	}
	if err := fn(d, parts[1:]...); err != nil {
		return err
	}
	return nil
}

func (d *Document) cmdAlign(args ...string) error {
	if len(args) == 0 {
		return errors.New("no alignment instruction")
	}
	switch args[0] {
	case "left", "l":
		d.align(alignLeft)
	case "right", "r":
		d.align(alignRight)
	case "center", "c":
		d.align(alignCenter)
	default:
		return fmt.Errorf("unknown alignment %q", args[0])
	}
	return nil
}

func (d *Document) align(a textAlign) {
	if d.alignment == a {
		return // already aligned
	}
	d.flush()
	d.alignment = a
}

func (d *Document) cmdImage(args ...string) error {
	const numArgs = 1
	if len(args) > numArgs {
		return fmt.Errorf("too many arguments, expected: %d", numArgs)
	}
	filename := args[0]
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	d.flush()
	d.c.AppendImage(img)
	return nil
}

const argSep = " "

func (d *Document) cmdFont(args ...string) error {
	if argc := len(args); argc < 1 || 2 < argc {
		return fmt.Errorf("invalid argument count, expected 1 or 2, provided: %d", argc)
	}
	var (
		fontOrFile = args[0]
		size       = 5.0 // default font size for TTF fonts to give 48 characcters per line
	)
	if len(args) > 1 {
		// parse size
		s, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			return err
		}
		if s < 0.0 {
			return fmt.Errorf("font size can't be negative: %f", s)
		}
		size = s
	}
	// if the font name doesn't have an extension, it must be a built-in, try load built in font
	if filepath.Ext(fontOrFile) == "" {
		face, err := fontmgr.LoadByName(fontOrFile)
		if err != nil {
			return err
		}
		d.font = face
		return nil
	} else {
		face, err := fontmgr.LoadFromFile(fontOrFile, size, d.dpi)
		if err != nil {
			return err
		}
		d.font = face
		return nil
	}
	// unreachable
}

// Image returns the document image.
func (d *Document) Image() image.Image {
	d.flush()
	return d.c.Image()
}
