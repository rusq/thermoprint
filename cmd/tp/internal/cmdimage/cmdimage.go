// Package cmdimage provides image printing subcommand.
package cmdimage

import (
	"context"
	"errors"
	"image"
	"os"

	"github.com/rusq/thermoprint/cmd/tp/internal/bootstrap"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
)

var CmdImage = &base.Command{
	Run:        runImage,
	UsageLine:  "tp image [flags] <image file>",
	Short:      "prints an image file",
	PrintFlags: true,
	Long: `
Prints an image.
`,
}

func runImage(ctx context.Context, cmd *base.Command, args []string) error {
	if len(args) != 1 {
		base.SetExitStatus(base.SInvalidParameters)
		return errors.New("expected only one image")
	}

	f, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}

	prn, err := bootstrap.Printer(ctx)
	if err != nil {
		return err
	}

	return prn.PrintImage(ctx, img)
}
