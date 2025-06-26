package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rusq/thermoprint"
	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
	"github.com/rusq/thermoprint/cmd/tp/internal/golang/base"
)

// Printer returns connected printer.
func Printer(ctx context.Context) (*thermoprint.LXD02, error) {
	if err := cfg.Adapter().Enable(); err != nil {
		return nil, fmt.Errorf("failed to enable Bluetooth adapter: %w", err)
	}
	prn, err := thermoprint.NewLXD02(ctx, cfg.Adapter(), cfg.SearchParams,
		thermoprint.WithEnergy(uint8(cfg.Energy)),
		thermoprint.WithPrintInterval(cfg.PrintDelay),
		thermoprint.WithCrop(cfg.Crop),
		thermoprint.WithDither(cfg.Dither),
		thermoprint.WithDryRun(cfg.DryRun),
		thermoprint.WithGamma(cfg.Gamma),
		thermoprint.WithAutoDither(cfg.AutoDither),
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
