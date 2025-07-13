package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"runtime/trace"
	"strings"

	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
	"github.com/rusq/thermoprint/cmd/tp/internal/cmdcompose"
	"github.com/rusq/thermoprint/cmd/tp/internal/cmdimage"
	"github.com/rusq/thermoprint/cmd/tp/internal/cmdpattern"
	"github.com/rusq/thermoprint/cmd/tp/internal/cmdserver"
	"github.com/rusq/thermoprint/cmd/tp/internal/cmdtext"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/help"
)

func init() {
	base.ThermoprintCommand.Commands = []*base.Command{
		// Add commands here.
		cmdimage.CmdImage,
		cmdtext.CmdText,
		cmdcompose.CmdCompose,
		cmdpattern.CmdPattern,
		cmdserver.CmdServer,
	}
}

func main() {
	flag.Usage = base.Usage
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		base.Usage()
		// Usage terminates the program.
		return
	}
	base.CmdName = args[0]
	if args[0] == "help" {
		help.Help(os.Stdout, args[1:])
		return
	}

BigCmdLoop:
	for bigCmd := base.ThermoprintCommand; ; {
		for _, cmd := range bigCmd.Commands {
			if cmd.Name() != args[0] {
				continue
			}
			if len(cmd.Commands) > 0 {
				bigCmd = cmd
				args = args[1:]
				if len(args) == 0 {
					help.PrintUsage(os.Stderr, bigCmd)
					base.SetExitStatus(base.SHelpRequested)
					base.Exit()
				}
				if args[0] == "help" {
					help.Help(os.Stdout, append(strings.Split(base.CmdName, " "), args[1:]...))
					return
				}
				base.CmdName += " " + args[0]
				continue BigCmdLoop
			}
			if !cmd.Runnable() {
				continue
			}
			if err := invoke(cmd, args); err != nil {
				msg := fmt.Sprintf("%03[1]d (%[1]s): %[2]s.", base.ExitStatus(), err)
				slog.Error(msg)
			}
			base.Exit()
			return
		}
		helpArg := ""
		if i := strings.LastIndex(base.CmdName, " "); i >= 0 {
			helpArg = " " + base.CmdName[:i]
		}
		fmt.Fprintf(os.Stderr, "tp %s: unknown command\nRun 'tp help%s' for usage.\n", base.CmdName, helpArg)
		base.SetExitStatus(base.SInvalidParameters)
		base.Exit()
	}
}

func init() {
	base.Usage = mainUsage
}

func mainUsage() {
	help.PrintUsage(os.Stderr, base.ThermoprintCommand)
	os.Exit(2)
}

func invoke(cmd *base.Command, args []string) error {
	if cmd.CustomFlags {
		args = args[1:]
	} else {
		var err error
		args, err = parseFlags(cmd, args)
		if err != nil {
			return err
		}
	}

	// maybe start trace
	if err := initTrace(cfg.TraceFile); err != nil {
		base.SetExitStatus(base.SGenericError)
		return fmt.Errorf("failed to start trace: %s", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	trapSigInfo()

	ctx, task := trace.NewTask(ctx, "command")
	defer task.End()

	// initialise default logging.
	if lg, err := initLog(cfg.LogFile, cfg.JSONHandler, cfg.Verbose); err != nil {
		return err
	} else {
		lg.With("command", cmd.Name())
		cfg.Log = lg
	}

	trace.Log(ctx, "command", fmt.Sprint("Running ", cmd.Name(), " command"))
	return cmd.Run(ctx, cmd, args)
}

func parseFlags(cmd *base.Command, args []string) ([]string, error) {
	cfg.SetBaseFlags(&cmd.Flag, cmd.FlagMask)
	cmd.Flag.Usage = func() { cmd.Usage() }
	if err := cmd.Flag.Parse(args[1:]); err != nil {
		return nil, err
	}
	return cmd.Flag.Args(), nil
}

// initTrace initialises the tracing.  If the filename is not empty, the file
// will be opened, trace will write to that file.  Returns the stop function
// that must be called in the deferred call.  If the error is returned the stop
// function is nil.
func initTrace(filename string) error {
	if filename == "" {
		return nil
	}

	slog.Debug("trace will be written to", "filename", filename)

	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	if err := trace.Start(f); err != nil {
		f.Close()
		slog.Warn("failed to start trace", "err", err)
		return nil
	}

	stop := func() {
		trace.Stop()
		if err := f.Close(); err != nil {
			slog.Warn("failed to close trace file", "filename", filename, "error", err)
		}
	}
	base.AtExit(stop)
	return nil
}

// initLog initialises the logging and returns the context with the Logger. If the
// filename is not empty, the file will be opened, and the logger output will
// be switch to that file. Returns the initialised logger, stop function and
// an error, if any. The stop function must be called in the deferred call, it
// will close the log file, if it is open. If the error is returned the stop
// function is nil.
func initLog(filename string, jsonHandler bool, verbose bool) (*slog.Logger, error) {
	if verbose {
		cfg.SetDebugLevel()
	}
	var opts = &slog.HandlerOptions{
		Level: iftrue(verbose, slog.LevelDebug, slog.LevelInfo),
	}
	if jsonHandler {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, opts)))
	}
	if filename != "" {
		slog.Debug("log messages will be written to file", "filename", filename)
		lf, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
		if err != nil {
			return slog.Default(), fmt.Errorf("failed to create the log file: %w", err)
		}
		log.SetOutput(lf) // redirect the standard log to the file just in case, panics will be logged there.

		var h slog.Handler = slog.NewTextHandler(lf, opts)
		if jsonHandler {
			h = slog.NewJSONHandler(lf, opts)
		}

		sl := slog.New(h)
		slog.SetDefault(sl)
		base.AtExit(func() {
			if err := lf.Close(); err != nil {
				slog.Warn("failed to close the log file", "err", err)
			}
		})
	}

	return slog.Default(), nil
}

func iftrue[T any](cond bool, t T, f T) T {
	if cond {
		return t
	}
	return f
}
