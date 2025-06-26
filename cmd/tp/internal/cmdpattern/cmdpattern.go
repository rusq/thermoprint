// Package cmdpattern provides pattern printing subcommand.
package cmdpattern

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/rusq/thermoprint/cmd/tp/internal/bootstrap"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
	"github.com/rusq/thermoprint/printers"
)

var CmdPattern = &base.Command{
	Run:        runPattern,
	UsageLine:  "tp pattern [flags] <pattern name>",
	Short:      "prints a test pattern",
	PrintFlags: true,
	Long: `
Prints a test pattern.
`,
}

var ListPatterns bool

func init() {
	CmdPattern.Flag.BoolVar(&ListPatterns, "list", false, "list patterns")
}

func runPattern(ctx context.Context, cmd *base.Command, args []string) error {
	if ListPatterns {
		return listPatterns(os.Stdout)
	}
	if len(args) != 1 {
		base.SetExitStatus(base.SInvalidParameters)
		listPatterns(os.Stderr)
		return errors.New("expected pattern name")
	}

	prn, err := bootstrap.Printer(ctx)
	if err != nil {
		return err
	}
	return prn.PrintPattern(ctx, args[0])
}

func listPatterns(w io.Writer) error {
	var names []string
	for name := range printers.TestImagePatterns {
		names = append(names, name)
	}
	for bufname := range printers.TestBufferPatterns {
		names = append(names, bufname)
	}
	slices.Sort(names)
	_, err := fmt.Fprintf(w, "Available test patterns: %v\n", names)
	return err
}
