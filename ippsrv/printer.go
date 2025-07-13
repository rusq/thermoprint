package ippsrv

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/google/uuid"

	"github.com/rusq/thermoprint"
)

var startTime = time.Now()

type testPrinter struct {
	Fullname string
	Id       string
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
	return &testPrinter{
		Fullname: fullname,
		Id:       id,
		state:    PSIdle, // Set initial state to idle
		Drv:      drv,
	}, nil
}

func (p *testPrinter) Name() string {
	return p.Id
}

func (p *testPrinter) MakeAndModel() string {
	return p.Fullname
}

func (p *testPrinter) Info() string {
	return p.Fullname // TODO
}

type PrinterState uint16

const (
	PSIdle PrinterState = iota + 3 // 3 is the value for idle in RFC 2911
	PSProcessing
	PSStopped
)

func (p *testPrinter) State() PrinterState {
	return p.state
}

func (p *testPrinter) Ready() bool {
	return true
}

func (p *testPrinter) UpTime() int {
	// https: //datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.4
	return int(time.Since(startTime).Seconds()) // returns seconds since start
}

func (p *testPrinter) MediaSupported() []string {
	return []string{"roll_57mm"}
}

func (p *testPrinter) MediaDefault() string {
	return "roll_57mm"
}

func (p *testPrinter) UUID() string {
	return uuid.NewSHA1(uuid.UUID{}, []byte(p.Fullname)).String()
}

func (p *testPrinter) Driver() Driver {
	return p.Drv
}

var ErrNoDriver = errors.New("no driver set for printer")

func (p *testPrinter) Print(ctx context.Context, data []byte) error {
	if p.Drv == nil {
		return ErrNoDriver
	}
	// TODO: handle data conversion.
	return errors.New("implement me") // p.Drv.PrintImage(ctx, img)
}

func (p *testPrinter) SetState(state PrinterState) {
	p.state = state
}
