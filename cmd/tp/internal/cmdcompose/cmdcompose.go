package cmdcompose

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/rusq/thermoprint/bitmap"
	"github.com/rusq/thermoprint/cmd/tp/internal/bootstrap"
	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
)

var CmdCompose = &base.Command{
	Run:        runCompose,
	UsageLine:  "tp compose [flags] <filename or ->",
	Short:      "compose image and text into a single printout",
	PrintFlags: true,
	Long: `
This is a sample command to get you started.
`,
}

var ditherText bool

func init() {
	CmdCompose.Flag.BoolVar(&ditherText, "dither-text", false, "dither text")
}

func runCompose(ctx context.Context, cmd *base.Command, args []string) error {
	if len(args) != 1 {
		base.SetExitStatus(base.SInvalidParameters)
		return errors.New("expected exactly one argument: filename or '-' for stdin")
	}

	filename := args[0]

	f := os.Stdin
	if filename != "-" {
		var err error
		f, err = os.Open(filename)
		if err != nil {
			base.SetExitStatus(base.SInvalidParameters)
			return fmt.Errorf("unable to open file %q: %w", filename, err)
		}
		defer f.Close()
	}
	prn, err := bootstrap.Printer(ctx)
	if err != nil {
		return err
	}
	dfn, ok := bitmap.DitherFunction(cfg.Dither)
	if !ok {
		return fmt.Errorf("unknown dithering function: %s", cfg.Dither)
	}
	c := bitmap.NewComposer(
		prn.Width(),
		bitmap.WithComposerCrop(cfg.Crop),
		bitmap.WithComposerDitherFunc(dfn),
		bitmap.WithComposerDitherText(ditherText),
	)

	doc := bitmap.NewDocument(c, prn.DPI())
	if err := doc.Parse(f); err != nil {
		base.SetExitStatus(base.SApplicationError)
		return err
	}
	img := doc.Image()

	return prn.PrintImage(ctx, img)
}
