package ippsrv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rusq/thermoprint"
	"github.com/rusq/thermoprint/bitmap"
)

var startTime = time.Now()

type basePrinter struct {
	Fullname string
	ID       string
	stateMu  sync.RWMutex
	state    PrinterState // Printer state, e.g., idle, processing, stopped
	printMu  sync.Mutex
	Drv      Driver
	Filter   Filter
}

type PrinterInformer interface {
	// Name should return a url-safe name for the printer (printer-name
	// attribute).
	// Example: "default" or "my-thermal-printer".
	Name() string
	// MakeAndModel should return the full name of the printer, including make
	// and model (printer-make-and-model attribute).
	// Example: "LX-D02 Thermal Printer".
	MakeAndModel() string
	// Info should return a human-readable description of the printer
	// (printer-info attribute).
	// Example: "LX-D02 Thermal Printer with USB and Bluetooth connectivity".
	Info() string
	// State should return the current state of the printer (printer-state
	// attribute), it can be one of the [PrinterState] constants.
	State() PrinterState
	// Ready should return true if the printer is ready to accept jobs. This is
	// used to determine if the printer is ready to print
	// (printer-is-accepting-jobs attribute).
	Ready() bool
	// UpTime should return the number of seconds since the printer was started.
	// This is used to report the printer uptime (printer-up-time attribute).
	UpTime() int
	// Media should return the media type used by the printer
	// (media-supported attribute).
	MediaSupported() []string
	// MediaDefault should return the default media type used by the printer
	// (media-default attribute).
	MediaDefault() string
	// SetState should set the printer state that is returned by the [State]
	// method.
	SetState(state PrinterState) // Set the printer state
	// UUID should return a unique identifier for the printer, used to identify
	// the printer in the system (printer-uuid attribute).
	UUID() string
}

type Printer interface {
	PrinterInformer

	// Print should print the given data to the printer.  Data can be in any
	// format, such as PostScript, PDF, or image. The method should handle
	// conversion to the printer's native format if necessary.
	Print(ctx context.Context, data []byte) error
	// Driver should return the driver used to print the data. The driver
	// should implement the [Driver] interface and handle the actual printing.
	Driver() Driver
}

type Driver interface {
	// SetOptions should set the options for the driver. Options can include
	// thermal printing options such as energy level, print delay, etc.
	// It should return an error if the options are invalid or cannot be set.
	SetOptions(opt ...thermoprint.Option) error
	// PrintImage should print the given image to the printer. The image can be
	// in any format and size, driver should handle the resizing and dithering.
	PrintImage(ctx context.Context, img image.Image) error
	// DPI should return the printer's DPI (dots per inch) setting, which is
	// used to determine the resolution of the printed output.
	DPI() float64
	// Width should return the width of the printer in pixels. This is used to
	// determine the width of the printed output.
	Width() int
}

type PrinterOption func(*basePrinter) error

func WithFilter(f Filter) PrinterOption {
	return func(p *basePrinter) error {
		if f == nil {
			return errors.New("filter cannot be nil")
		}
		p.Filter = f
		return nil
	}
}

func WrapDriver(drv Driver, id, fullname string, opt ...PrinterOption) (Printer, error) {
	if drv == nil {
		return nil, errors.New("driver cannot be nil")
	}
	if fullname == "" {
		return nil, errors.New("printer fullname cannot be empty")
	}
	if id == "" {
		return nil, errors.New("printer ID cannot be empty")
	}
	p := &basePrinter{
		Fullname: fullname,
		ID:       id,
		state:    PSIdle, // Set initial state to idle
		Drv:      drv,
		// Default filter: PWG/URF raster streams are decoded natively,
		// anything else falls back to ImageMagick. Can be overridden.
		Filter: &rasterSniffFilter{fallback: &imageMagickFilter{}},
	}
	for _, o := range opt {
		if err := o(p); err != nil {
			return nil, fmt.Errorf("failed to apply printer option: %w", err)
		}
	}
	return p, nil
}

func (p *basePrinter) Name() string {
	return p.ID
}

func (p *basePrinter) MakeAndModel() string {
	return p.Fullname
}

func (p *basePrinter) Info() string {
	return p.Fullname // TODO
}

type PrinterState uint16

//go:generate go tool stringer -trimprefix PS -type PrinterState
const (
	PSIdle PrinterState = iota + 3 // 3 is the value for idle in RFC 2911
	PSProcessing
	PSStopped
)

func (p *basePrinter) State() PrinterState {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	return p.state
}

func (p *basePrinter) Ready() bool {
	return true
}

func (p *basePrinter) UpTime() int {
	// https: //datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.4
	return int(time.Since(startTime).Seconds()) // returns seconds since start
}

// Media names are PWG 5101.1 self-describing sizes at the PRINTABLE width:
// the label stock is 58mm, but the head prints 384px @ 203dpi ≈ 48mm.
// Advertising 48mm makes driverless clients rasterise at exactly 384px, so
// pages arrive pixel-perfect without rescaling.
func (p *basePrinter) MediaSupported() []string {
	return []string{
		"om_label-48x100mm_48x100mm",
		"om_label-48x40mm_48x40mm",
		"om_label-48x32mm_48x32mm",
		"om_label-48x60mm_48x60mm",
		rollCustomMinMedia,
		rollCustomMaxMedia,
	}
}

func (p *basePrinter) MediaDefault() string {
	return "om_label-48x100mm_48x100mm"
}

func (p *basePrinter) UUID() string {
	return uuid.NewSHA1(uuid.UUID{}, []byte(p.Fullname)).String()
}

func (p *basePrinter) Driver() Driver {
	return p.Drv
}

var (
	// ErrNoDriver is returned when the printer does not have a driver set.
	ErrNoDriver = errors.New("no driver set for printer")
	// ErrEmptyData is returned when the data to print is empty.
	ErrEmptyData = errors.New("data cannot be empty")
	// ErrNoImages is returned when the data could not be converted to any
	// images.
	ErrNoImages = errors.New("no images were converted from the data")
	// ErrPrintOptionsUnsupported is returned when a printer cannot honor
	// options attached to an IPP print job.
	ErrPrintOptionsUnsupported = errors.New("printer does not support print job options")
)

// PrintOptions are optional behaviors requested for a single IPP print job.
type PrintOptions struct {
	// TrimTrailingBlank removes trailing blank rows for variable-height roll
	// jobs so the printer stops after the visible content.
	TrimTrailingBlank bool
}

// OptionPrinter is implemented by printers that can honor per-job print
// options supplied by the IPP server.
type OptionPrinter interface {
	PrintWithOptions(ctx context.Context, data []byte, opts PrintOptions) error
}

type printJobOptions struct {
	trimTrailingBlank bool
}

func (p *basePrinter) Print(ctx context.Context, data []byte) error {
	return p.print(ctx, data, printJobOptions{})
}

func (p *basePrinter) PrintWithOptions(ctx context.Context, data []byte, opts PrintOptions) error {
	return p.print(ctx, data, printJobOptions{trimTrailingBlank: opts.TrimTrailingBlank})
}

func (p *basePrinter) print(ctx context.Context, data []byte, opts printJobOptions) error {
	p.printMu.Lock()
	defer p.printMu.Unlock()

	if p.Drv == nil {
		return ErrNoDriver
	}
	if len(data) == 0 {
		return ErrEmptyData
	}

	// try decoding the data as an image
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		// fast path for images
		return p.printImage(ctx, img, opts)
	}

	// slow path for other data formats
	// multiple formats can be supported, such as PostScript, PDF, etc.
	images, err := p.Filter.ToRaster(ctx, int(p.Drv.DPI()), data)
	if err != nil {
		slog.Error("images", "len", len(images), "err", err)
		return fmt.Errorf("failed to convert data: %w", err)
	}
	if len(images) == 0 {
		return ErrNoImages
	}
	slog.Debug("converted source document", "pages", len(images), "dpi", p.Drv.DPI())

	// combine all pages into a long image.
	c := bitmap.NewComposer(p.Drv.Width(), bitmap.WithComposerDitherFunc(bitmap.DitherDefault))
	for _, img := range images {
		if bitmap.IsDocument(img, 50, 200) {
			c.AppendImageDither(img, bitmap.DitherThresholdFn(128))
		} else {
			c.AppendImage(img)
		}
	}
	// print the image.
	img := c.Image()
	return p.printImage(ctx, img, opts)
}

func (p *basePrinter) printImage(ctx context.Context, img image.Image, opts printJobOptions) error {
	if opts.trimTrailingBlank {
		img = trimTrailingBlankRows(img)
	}
	if err := p.Drv.PrintImage(ctx, img); err != nil {
		return fmt.Errorf("failed to print image: %w", err)
	}
	return nil
}

func printWithOptions(ctx context.Context, p Printer, data []byte, opts printJobOptions) error {
	if p, ok := p.(OptionPrinter); ok {
		return p.PrintWithOptions(ctx, data, PrintOptions{TrimTrailingBlank: opts.trimTrailingBlank})
	}
	if opts.trimTrailingBlank {
		return ErrPrintOptionsUnsupported
	}
	return p.Print(ctx, data)
}

func trimTrailingBlankRows(img image.Image) image.Image {
	b := img.Bounds()
	if b.Empty() || b.Dy() <= 1 {
		return img
	}
	last := b.Min.Y
	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		if !isBlankRow(img, y) {
			last = y
			break
		}
	}
	height := last - b.Min.Y + 1
	if height == b.Dy() {
		return img
	}
	trimmed := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+height)
	if img, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return img.SubImage(trimmed)
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), height))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

func isBlankRow(img image.Image, y int) bool {
	b := img.Bounds()
	for x := b.Min.X; x < b.Max.X; x++ {
		if colorToWhiteBackgroundGray(img.At(x, y)) != 255 {
			return false
		}
	}
	return true
}

func colorToWhiteBackgroundGray(c color.Color) uint8 {
	if gray, ok := c.(color.Gray); ok {
		return gray.Y
	}
	r, g, b, a := c.RGBA()
	r += 0xffff - a
	g += 0xffff - a
	b += 0xffff - a
	gray := (299*r + 587*g + 114*b) / 1000
	return uint8(gray >> 8)
}

func (p *basePrinter) SetState(state PrinterState) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	p.state = state
}
