package cmdserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/rusq/thermoprint/cmd/tp/internal/bootstrap"
	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
	"github.com/rusq/thermoprint/ippsrv"
	"golang.org/x/term"
)

var CmdServer = &base.Command{
	Run:        runServer,
	UsageLine:  "tp server [flags]",
	Short:      "start the IPP server",
	PrintFlags: true,
	Long: `
Starts the IPP server that accepts print jobs for the connected printer.

By default the printer is advertised on the local network via Bonjour/DNS-SD,
so it appears in e.g. macOS "Printers & Scanners" -> Add Printer.  For the
advertisement to work, the server must listen on a non-loopback address:
binding to a loopback address (e.g. -addr localhost:6310) disables it.
`,
}

var (
	addr         string
	protoDumpDir string
	noMDNS       bool
	noTUI        bool
)

func init() {
	CmdServer.Flag.StringVar(&addr,
		"addr",
		":6310",
		"address to listen on; bind a non-loopback address to be discoverable on the network")
	CmdServer.Flag.StringVar(&protoDumpDir,
		"dumpdir",
		"",
		"directory for protocol dumps; if not specified, a temporary directory will be used")
	CmdServer.Flag.BoolVar(&noMDNS,
		"no-mdns",
		false,
		"disable Bonjour/DNS-SD printer advertisement")
	CmdServer.Flag.BoolVar(&noTUI,
		"no-tui",
		false,
		"disable the interactive dashboard and keep plain log output")
}

func runServer(ctx context.Context, cmd *base.Command, args []string) error {
	if len(args) > 0 {
		base.SetExitStatus(base.SInvalidParameters)
		return fmt.Errorf("unexpected arguments: %v", args)
	}
	p, err := bootstrap.Printer(ctx)
	if err != nil {
		base.SetExitStatus(base.SApplicationError)
		return fmt.Errorf("failed to get printer: %w", err)
	}
	ippPrn, err := ippsrv.WrapDriver(p, "default", "LX-D02 Thermal Printer")
	if err != nil {
		base.SetExitStatus(base.SApplicationError)
		return fmt.Errorf("failed to wrap printer: %w", err)
	}
	var opts = []ippsrv.Option{
		ippsrv.WithDebug(cfg.Verbose),
		ippsrv.WithDumpDir(protoDumpDir),
	}
	if !noMDNS {
		opts = append(opts, ippsrv.WithBonjour())
	}
	s, err := ippsrv.New(ippPrn, opts...)
	if err != nil {
		base.SetExitStatus(base.SApplicationError)
		return err
	}
	cfg.RegisterSigInfoReporter(s.Info)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-runCtx.Done()
		if err := s.Shutdown(context.Background()); err != nil {
			slog.Error("error shutting down server", "err", err)
		} else {
			slog.Info("server shut down successfully")
		}
	}()

	if useTUI := shouldUseTUI(noTUI, os.Stdout.Fd(), os.Stderr.Fd()); useTUI {
		logs := newLogBuffer(400)
		installTUILogger(logs, cfg.LogFile != "")
		if cfg.LogFile == "" {
			log.SetOutput(logs.Writer())
		}
		serverResult := newServerResult()
		go func() {
			slog.Info("starting server", "addr", addr)
			serverResult.finish(listenAndServe(s, addr))
		}()
		if err := runDashboard(runCtx, s, p, logs, serverResult); err != nil {
			base.SetExitStatus(base.SApplicationError)
			return err
		}
		cancel()
		if err := serverResult.wait(); err != nil {
			base.SetExitStatus(base.SApplicationError)
			return err
		}
		return nil
	}

	slog.Info("starting server", "addr", addr)
	if err := listenAndServe(s, addr); err != nil {
		base.SetExitStatus(base.SApplicationError)
		return err
	}
	return nil
}

func listenAndServe(s *ippsrv.Server, addr string) error {
	if err := s.ListenAndServe(addr); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		slog.Error("error starting server", "err", err)
		return err
	}
	return nil
}

func shouldUseTUI(disabled bool, stdoutFD, stderrFD uintptr) bool {
	return modeAllowsTUI(disabled, term.IsTerminal(int(stdoutFD)), term.IsTerminal(int(stderrFD)))
}

func modeAllowsTUI(disabled, stdoutTTY, stderrTTY bool) bool {
	return !disabled && stdoutTTY && stderrTTY
}
