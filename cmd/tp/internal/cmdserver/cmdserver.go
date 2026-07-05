package cmdserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rusq/thermoprint/cmd/tp/internal/bootstrap"
	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
	"github.com/rusq/thermoprint/ippsrv"
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
	go func() {
		<-ctx.Done()
		if err := s.Shutdown(context.Background()); err != nil {
			slog.Error("error shutting down server", "err", err)
		} else {
			slog.Info("server shut down successfully")
		}
	}()

	slog.Info("starting server", "addr", addr)
	if err := s.ListenAndServe(addr); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		slog.Error("error starting server", "err", err)
	}
	return nil
}
