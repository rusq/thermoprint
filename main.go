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
	energy     uint // 0-6
	printDelay time.Duration
	imageFile  string
	text       string
	verbose    bool
}

var cliflags config

func init() {
	os.Setenv("DEBUG", "1") // Set DEBUG environment variable for verbose logging
	flag.StringVar(&cliflags.Name, "p", "LX-D02", "Printer name to use")
	flag.StringVar(&cliflags.MACAddress, "mac", "", "MAC address of the printer")
	flag.BoolVar(&cliflags.verbose, "v", os.Getenv("DEBUG") == "1", "Enable verbose logging")
	flag.UintVar(&cliflags.energy, "e", 2, "Thermal energy `level` (0-6), higher is darker printout")
	flag.DurationVar(&cliflags.printDelay, "d", printers.DefaultPrintDelay, "Delay between print commands")
	flag.StringVar(&cliflags.imageFile, "i", "", "Image file to print (PNG or JPEG)")
	flag.StringVar(&cliflags.text, "t", "", "Text to print (overrides image file)")
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

	if cliflags.imageFile == "" && cliflags.text == "" {
		flag.Usage()
		log.Fatal("You must specify either an image file with -i or text to print with -t")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cliflags); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config) error {
	prn, err := printers.NewLXD02(ctx, adapter, cfg.SearchParameters, printers.WithEnergy(uint8(cfg.energy)), printers.WithPrintInterval(cfg.printDelay))
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
		return prn.PrintText(ctx, cfg.text)
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
	}
	return fmt.Errorf("no valid input provided, either image or text must be specified")
}
