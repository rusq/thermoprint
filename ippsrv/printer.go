package ippsrv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/rusq/thermoprint"
	"github.com/rusq/thermoprint/bitmap"
)

var startTime = time.Now()

type basePrinter struct {
	Fullname string
	ID       string
	state    PrinterState // Printer state, e.g., idle, processing, stopped
	Drv      Driver
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

func WrapDriver(drv Driver, id, fullname string) (Printer, error) {
	if drv == nil {
		return nil, errors.New("driver cannot be nil")
	}
	if fullname == "" {
		return nil, errors.New("printer fullname cannot be empty")
	}
	if id == "" {
		return nil, errors.New("printer ID cannot be empty")
	}
	return &basePrinter{
		Fullname: fullname,
		ID:       id,
		state:    PSIdle, // Set initial state to idle
		Drv:      drv,
	}, nil
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

const (
	PSIdle PrinterState = iota + 3 // 3 is the value for idle in RFC 2911
	PSProcessing
	PSStopped
)

func (p *basePrinter) State() PrinterState {
	return p.state
}

func (p *basePrinter) Ready() bool {
	return true
}

func (p *basePrinter) UpTime() int {
	// https: //datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.4
	return int(time.Since(startTime).Seconds()) // returns seconds since start
}

func (p *basePrinter) MediaSupported() []string {
	return []string{"roll_57mm"}
}

func (p *basePrinter) MediaDefault() string {
	return "roll_57mm"
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
)

func (p *basePrinter) Print(ctx context.Context, data []byte) error {
	if p.Drv == nil {
		return ErrNoDriver
	}
	if len(data) == 0 {
		return ErrEmptyData
	}

	// try decoding the data as an image
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		// fast path for images
		return p.Drv.PrintImage(ctx, img)
	}

	// slow path for other data formats
	// multiple formats can be supported, such as PostScript, PDF, etc.
	images, err := convertWithMagick(ctx, int(p.Drv.DPI()), data)
	if err != nil {
		slog.Error("images", "len", len(images), "err", err)
		return fmt.Errorf("failed to convert data: %w", err)
	}
	if len(images) == 0 {
		return errors.New("no images were converted from the data")
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
	if err := p.Drv.PrintImage(ctx, c.Image()); err != nil {
		return fmt.Errorf("failed to print image: %w", err)
	}
	return nil
}

// convertWithMagick converts the given data, returning a list of converted
// file paths, where each file path is a converted page of the document in PNG format.
func convertWithMagick(ctx context.Context, dpi int, data []byte) ([]image.Image, error) {
	cmd := exec.CommandContext(ctx, "magick", "-", "-density", strconv.Itoa(dpi), "-background", "white", "-alpha", "remove", "png:-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var images []image.Image
	r := bytes.NewReader(out)
	var eos bool // end of stream flag
	for !eos {
		slog.Info("decoding image from magick output")
		img, err := png.Decode(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// End of the stream, no more images to decode
				break
			}
			return images, fmt.Errorf("failed to decode image: %w", err)
		}
		images = append(images, img)
		currPos, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return images, fmt.Errorf("failed to seek in output stream: %w", err)
		}
		if currPos >= int64(len(out)) {
			eos = true // reached the end of the output stream
		}
	}

	return images, nil
}

func (p *basePrinter) SetState(state PrinterState) {
	p.state = state
}
