// Package cmdtext provides a text printing subcommand.
package cmdtext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/image/font"

	"github.com/rusq/thermoprint/cmd/tp/internal/bootstrap"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
	"github.com/rusq/thermoprint/fontmgr"
	"github.com/rusq/thermoprint/printers"
)

var CmdText = &base.Command{
	Run:        runText,
	UsageLine:  "tp text [flags] <filename or - for stdin>",
	Short:      "prints text",
	PrintFlags: true,
	Long: `
Prints the text from the specified file or from stdin if '-' is used.
`,
}

var (
	FontFile    string
	FontName    string
	ListFonts   bool
	TTFFontSize float64
	TTFDPI      float64
)

func init() {
	CmdText.Flag.StringVar(&FontFile, "font-file", "", "font `filename` (overrides -font)")
	CmdText.Flag.StringVar(&FontName, "font", "toshiba", "select a built-in font `name`")
	CmdText.Flag.BoolVar(&ListFonts, "list-fonts", false, "lists built-in fonts")
	CmdText.Flag.Float64Var(&TTFFontSize, "font-size", 5.0, "font size in `pt` for true-type fonts")
	CmdText.Flag.Float64Var(&TTFDPI, "dpi", float64(printers.LXD02Rasteriser.Dpi), "DPI for TrueType fonts")
}

func runText(ctx context.Context, cmd *base.Command, args []string) error {
	if ListFonts {
		return listFonts(os.Stdout)
	}
	if len(args) != 1 {
		base.SetExitStatus(base.SInvalidParameters)
		return errors.New("expected exactly one argument")
	}

	text := args[0]

	var face font.Face
	if FontFile != "" {
		fc, err := fontmgr.LoadFromFile(FontFile, TTFFontSize, TTFDPI)
		if err != nil {
			return err
		}
		face = fc
	} else {
		fc, err := fontmgr.LoadByName(FontName)
		if err != nil {
			return err
		}
		face = fc
	}
	if text == "-" {
		// Read text from stdin if "-" is specified
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(os.Stdin); err != nil {
			return fmt.Errorf("failed to read text from stdin: %w", err)
		}
		text = buf.String()
	}

	prn, err := bootstrap.Printer(ctx)
	if err != nil {
		return err
	}

	return prn.PrintTextTTF(ctx, text, face)
}

func listFonts(w io.Writer) error {
	if err := fontmgr.LoadFontCatalogue(func(fnt fontmgr.BitmapFont, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%20s (%dx%d)\n", fnt.Name, fnt.Width, fnt.Height)
		return nil
	}); err != nil {
		return err
	}
	return nil
}
