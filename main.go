package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tinygo.org/x/bluetooth"

	"github.com/rusq/thermoprint/printers"
)

var adapter = bluetooth.DefaultAdapter

type config struct {
	printers.SearchParameters
	energy         uint // 0-6
	printDelay     time.Duration
	imageFile      string
	text           string
	pattern        string  // test pattern to print
	crop           bool    // crop image to printer width instead of resizing
	dither         string  // dithering algorithm to use
	ttf            bool    // use true type font for text printing
	ttfFontSize    float64 // font size for TTF text printing, default is 8pt TODO
	ttfLineSpacing float64 // line spacing for TTF text printing, default is 1.0 TODO
	dry            bool    // dry run, do not send commands to printer
	verbose        bool
}

var cliflags config

func init() {
	flag.StringVar(&cliflags.Name, "p", "LX-D02", "Printer name to use")
	flag.StringVar(&cliflags.MACAddress, "mac", "", "MAC address of the printer")
	flag.BoolVar(&cliflags.verbose, "v", os.Getenv("DEBUG") == "1", "Enable verbose logging")
	flag.UintVar(&cliflags.energy, "e", 2, "Thermal energy `level` (0-6), higher is darker printout")
	flag.DurationVar(&cliflags.printDelay, "d", printers.DefaultPrintDelay, "Delay between print commands")
	flag.StringVar(&cliflags.imageFile, "i", "", "Image file to print (PNG or JPEG)")
	flag.StringVar(&cliflags.text, "t", "", "Text to print (overrides image file)")
	flag.StringVar(&cliflags.pattern, "pattern", "", "Test pattern to print (e.g. 'LastLineTest')")
	flag.BoolVar(&cliflags.crop, "crop", false, "Crop image to printer width instead of resizing")
	flag.StringVar(&cliflags.dither, "dither", "", fmt.Sprintf("Dithering algorithm to use, one of: %v", printers.AllDitherFunctions()))
	flag.BoolVar(&cliflags.ttf, "ttf", false, "Use TrueType font for text printing (requires -t option)")
	flag.BoolVar(&cliflags.dry, "dry", false, "Dry run, do not send commands to printer")
}

func init() {
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, "Usage: %s [flags] <-i image | -t \"text to print\">\n", os.Args[0])
		flag.PrintDefaults()
	}
}

func main() {
	if err := adapter.Enable(); err != nil {
		log.Fatalf("Failed to enable Bluetooth adapter: %v", err)
	}

	flag.Parse()
	if cliflags.verbose {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	if cliflags.imageFile == "" && cliflags.text == "" && cliflags.pattern == "" {
		flag.Usage()
		log.Fatal("You must specify either an image file with -i or text to print with -t")
	}
	if cliflags.text == "" && cliflags.ttf {
		slog.Warn("TrueType font option -ttf is set, but no text provided. It will be ignored.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cliflags); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config) error {
	prn, err := printers.NewLXD02(ctx, adapter, cfg.SearchParameters,
		printers.WithEnergy(uint8(cfg.energy)),
		printers.WithPrintInterval(cfg.printDelay),
		printers.WithCrop(cfg.crop),
		printers.WithDither(cfg.dither),
		printers.WithDryRun(cfg.dry),
	)
	if err != nil {
		return fmt.Errorf("failed to create printer: %w", err)
	}
	defer prn.Disconnect()
	if cfg.text != "" {
		if cfg.text == "-" {
			// Read text from stdin if "-" is specified
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(os.Stdin); err != nil {
				return fmt.Errorf("failed to read text from stdin: %w", err)
			}
			cfg.text = buf.String()
		}
		if cfg.ttf {
			return prn.PrintTextTTF(ctx, cfg.text, cfg.ttfFontSize, cfg.ttfLineSpacing)
		} else {
			return prn.PrintText(ctx, cfg.text)
		}
	} else if cfg.imageFile != "" {
		f, err := os.Open(cfg.imageFile)
		if err != nil {
			return fmt.Errorf("failed to open image file: %w", err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			return fmt.Errorf("failed to decode image: %w", err)
		}
		return prn.PrintImage(ctx, img)
	} else if cfg.pattern != "" {
		// TODO: this should be a method on the printer
		return prn.PrintPattern(ctx, cfg.pattern)

	}
	return fmt.Errorf("no valid input provided, either image or text must be specified")
}
