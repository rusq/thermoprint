package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
	"github.com/rusq/thermoprint/printers"
)

// Printer returns connected printer.
func Printer(ctx context.Context) (*printers.LXD02, error) {
	if err := cfg.Adapter().Enable(); err != nil {
		return nil, fmt.Errorf("failed to enable Bluetooth adapter: %w", err)
	}
	prn, err := printers.NewLXD02(ctx, cfg.Adapter(), cfg.SearchParams,
		printers.WithEnergy(uint8(cfg.Energy)),
		printers.WithPrintInterval(cfg.PrintDelay),
		printers.WithCrop(cfg.Crop),
		printers.WithDither(cfg.Dither),
		printers.WithDryRun(cfg.DryRun),
		printers.WithGamma(cfg.Gamma),
		printers.WithAutoDither(cfg.AutoDither),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create printer: %w", err)
	}
	base.AtExit(func() {
		if err := prn.Disconnect(); err != nil {
			slog.ErrorContext(ctx, "error disconnecting from printer", "error", err)
		}
	})
	return prn, nil
}
