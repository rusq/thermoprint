// Package cfg contains common configuration variables.
package cfg

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"tinygo.org/x/bluetooth"

	"github.com/rusq/thermoprint"
)

var adapter = bluetooth.DefaultAdapter

var (
	TraceFile   string = os.Getenv("TRACE_FILE")
	LogFile     string = os.Getenv("LOG_FILE")
	JSONHandler bool   = os.Getenv("JSON_LOG") != ""
	Verbose     bool   = os.Getenv("DEBUG") != ""

	SearchParams thermoprint.SearchParameters
	Energy       uint
	PrintDelay   time.Duration
	DryRun       bool = os.Getenv("DRY_RUN") == "1"

	Gamma      float64
	Crop       bool
	Dither     string
	AutoDither bool

	Log *slog.Logger = slog.Default()
)

type FlagMask uint16

const (
	DefaultFlags     FlagMask = 0
	OmitConnectFlags FlagMask = 1 << (iota - 1)
	OmitCommonImageFlags

	OmitAll = OmitConnectFlags | OmitCommonImageFlags
)

// SetBaseFlags sets base flags.
func SetBaseFlags(fs *flag.FlagSet, mask FlagMask) {
	fs.StringVar(&TraceFile, "trace", TraceFile, "trace `filename`")
	fs.StringVar(&LogFile, "log", LogFile, "log `file`, if not specified, messages are printed to STDERR")
	fs.BoolVar(&JSONHandler, "log-json", JSONHandler, "log in JSON format")
	fs.BoolVar(&Verbose, "v", Verbose, "verbose messages")

	if mask&OmitConnectFlags == 0 {
		fs.StringVar(&SearchParams.Name, "p", "LX-D02", "Printer name to use")
		fs.StringVar(&SearchParams.MACAddress, "mac", "", "MAC address of the printer")
		fs.UintVar(&Energy, "e", 2, "Thermal energy `level` (0-6), higher is darker printout")
		fs.DurationVar(&PrintDelay, "d", thermoprint.DefaultPrintDelay, "Delay between print commands")
		fs.BoolVar(&DryRun, "dry", DryRun, "dry run, do not print, but create preview files")
	}

	if mask&OmitCommonImageFlags == 0 {
		fs.Float64Var(&Gamma, "gamma", thermoprint.DefaultGamma, "Gamma correction for dithering")
		fs.BoolVar(&Crop, "crop", false, "Crop image to printer width instead of resizing")
		fs.StringVar(&Dither, "dither", "", fmt.Sprintf("Dithering algorithm to use, one of: %v", thermoprint.AllDitherFunctions()))
		fs.BoolVar(&AutoDither, "auto-dither", false, "automatically disables dithering if a document is detected")
	}
}

func Adapter() *bluetooth.Adapter {
	return adapter
}
