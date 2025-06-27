package dosomething

import (
	"context"
	"errors"
	"fmt"
	"os"

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

func init() {
	CmdDoSomething.Flag.StringVar(&CustomFlag, "custom-flag", "", "custom flag is different than the global flags")
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

	img, err := thermoprint.ParseComposeScript(f)
	if err != nil {
		base.SetExitStatus(base.SApplicationError)
		return fmt.Errorf("unable to parse compose script: %w", err)
	}
	if cfg.DryRun {
		fmt.Println("Dry run: would print the following image:")
		fmt.Println(img)
		return nil
	}

	return nil
}
