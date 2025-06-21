package main

import (
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
}

func init() {
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, "Usage: %s [flags] <image>\n", os.Args[0])
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

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	imagefile := flag.Arg(0)
	if err := run(ctx, cliflags, imagefile); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config, imgfile string) error {
	prn, err := printers.NewLXD02(ctx, adapter, cfg.SearchParameters, printers.WithEnergy(uint8(cfg.energy)), printers.WithPrintInterval(cfg.printDelay))
	if err != nil {
		return fmt.Errorf("failed to create printer: %w", err)
	}
	defer prn.Disconnect()
	f, err := os.Open(imgfile)
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
