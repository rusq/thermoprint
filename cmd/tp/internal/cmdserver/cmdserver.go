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
This is a sample command to get you started.
`,
}

var addr string

func init() {
	CmdServer.Flag.StringVar(&addr, "addr", "localhost:6310", "custom flag is different than the global flags")
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
	ippPrn, err := ippsrv.WrapDriver(p, "default", "Thermal Printer")
	if err != nil {
		base.SetExitStatus(base.SApplicationError)
		return fmt.Errorf("failed to wrap printer: %w", err)
	}
	s, err := ippsrv.New(ippPrn)
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
