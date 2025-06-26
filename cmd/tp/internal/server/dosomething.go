package dosomething

import (
	"context"
	"fmt"
	"time"

	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
)

var CmdDoSomething = &base.Command{
	Run:       runDoSomething,
	UsageLine: "tp dosomething [flags]",
	Short:     "does something, does not take any arguments",
	// FlagMask:   cfg.OmitSomeFlag, // but not the some other flag.
	PrintFlags: true,
	Long: `
This is a sample command to get you started.
`,
}

var CustomFlag string

func init() {
	CmdDoSomething.Flag.StringVar(&CustomFlag, "custom-flag", "", "custom flag is different than the global flags")
}

func runDoSomething(ctx context.Context, cmd *base.Command, args []string) error {
	if len(args) > 0 {
		base.SetExitStatus(base.SInvalidParameters)
		return fmt.Errorf("unexpected arguments: %v", args)
	}

	fmt.Println("Did something.")
	fmt.Println("Custom flag:", CustomFlag)
	// fmt.Println("Some flag (global):", cfg.SomeFlag)
	fmt.Println("Now is:", time.Now())

	return nil
}
