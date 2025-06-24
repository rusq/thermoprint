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

	"golang.org/x/image/font"
	"tinygo.org/x/bluetooth"

	"github.com/rusq/thermoprint/printers"
)

var adapter = bluetooth.DefaultAdapter

type config struct {
	printers.SearchParameters
	energy      uint // 0-6
	printDelay  time.Duration
	imageFile   string
	text        string
	pattern     string  // test pattern to print
	crop        bool    // crop image to printer width instead of resizing
	dither      string  // dithering algorithm to use
	fontFile    string  // if defined, loads external font
	fontName    string  // select built in font
	listFonts   bool    // lists built-in fonts
	ttfFontSize float64 // font size for TTF text printing, default is 8pt, ignored for bitmap fonts
	ttfDPI      float64 // default dpi for TTF rasterisation
	dry         bool    // dry run, do not send commands to printer
	gamma       float64 // gamma correction for dithering, default is 0.0
	verbose     bool
	autoDither  bool
}

var cliflags config

func init() {
	// application flags
	flag.BoolVar(&cliflags.verbose, "v", os.Getenv("DEBUG") == "1", "Enable verbose logging")
	flag.BoolVar(&cliflags.dry, "dry", false, "Dry run, do not send commands to printer")

	// printer
	flag.StringVar(&cliflags.Name, "p", "LX-D02", "Printer name to use")
	flag.StringVar(&cliflags.MACAddress, "mac", "", "MAC address of the printer")
	flag.UintVar(&cliflags.energy, "e", 2, "Thermal energy `level` (0-6), higher is darker printout")
	flag.DurationVar(&cliflags.printDelay, "d", printers.DefaultPrintDelay, "Delay between print commands")

	// pattern
	flag.StringVar(&cliflags.pattern, "pattern", "", "Test pattern to print (e.g. 'LastLineTest')")

	// image
	flag.StringVar(&cliflags.imageFile, "i", "", "Image file to print (PNG or JPEG)")
	flag.StringVar(&cliflags.dither, "dither", "", fmt.Sprintf("Dithering algorithm to use, one of: %v", printers.AllDitherFunctions()))
	flag.Float64Var(&cliflags.gamma, "gamma", printers.DefaultGamma, "Gamma correction for dithering")

	// text
	flag.StringVar(&cliflags.text, "t", "", "Text to print (overrides image file)")
	flag.BoolVar(&cliflags.crop, "crop", false, "Crop image to printer width instead of resizing")
	flag.StringVar(&cliflags.fontFile, "font-file", "", "font `filename` (overrides -font)")
	flag.StringVar(&cliflags.fontName, "font", "toshiba", "select a built-in font `name`")
	flag.BoolVar(&cliflags.listFonts, "list-fonts", false, "lists built-in fonts")
	flag.Float64Var(&cliflags.ttfFontSize, "font-size", 5.0, "font size in `pt` for true-type fonts")
	flag.Float64Var(&cliflags.ttfDPI, "dpi", float64(printers.LXD02Rasteriser.Dpi), "DPI for TrueType fonts")
	flag.BoolVar(&cliflags.autoDither, "auto-dither", false, "automatically disables dithering if a document is detected")
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
	if cliflags.listFonts {
		if err := listFonts(os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	if cliflags.imageFile == "" && cliflags.text == "" && cliflags.pattern == "" {
		flag.Usage()
		log.Fatal("one of -i, -t, -p or -list-fonts is expected")
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
		printers.WithGamma(cfg.gamma),
		printers.WithAutoDither(cfg.autoDither),
	)
	if err != nil {
		return fmt.Errorf("failed to create printer: %w", err)
	}
	defer prn.Disconnect()
	if cfg.text != "" {
		var face font.Face
		if cfg.fontFile != "" {
			fc, err := loadFontfile(cfg.fontFile, cfg.ttfFontSize, cfg.ttfDPI)
			if err != nil {
				return err
			}
			face = fc
		} else {
			fc, err := loadFntByName(cfg.fontName)
			if err != nil {
				return err
			}
			face = fc
		}
		if cfg.text == "-" {
			// Read text from stdin if "-" is specified
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(os.Stdin); err != nil {
				return fmt.Errorf("failed to read text from stdin: %w", err)
			}
			cfg.text = buf.String()
		}
		return prn.PrintTextTTF(ctx, cfg.text, face)
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
