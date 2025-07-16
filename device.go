package thermoprint

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tinygo.org/x/bluetooth"
)

const retryWaitTime = 1 * time.Second

type SearchParameters struct {
	Name       string
	MACAddress string
}

func connectWithRetries(ctx context.Context, adapter *bluetooth.Adapter, sp SearchParameters, maxRetries int) (bluetooth.Device, error) {
	var device bluetooth.Device
	var lastErr error
	retries := 0
	for retries < maxRetries {
		foundDevice, err := locateDevice(ctx, adapter, sp)
		if err != nil {
			return bluetooth.Device{}, fmt.Errorf("failed to locate device: %w", err)
		}

		dev, err := adapter.Connect(foundDevice.Address, bluetooth.ConnectionParams{})
		lastErr = err
		if err == nil {
			device = dev
			break
		}
		retries++
		lastErr = err
		slog.Warn("Failed to connect to device, retrying", "attempt", retries, "error", err)
		time.Sleep(retryWaitTime) // Wait before retrying
	}
	if lastErr != nil {
		return bluetooth.Device{}, fmt.Errorf("failed to connect to device: %w", lastErr)
	}
	return device, nil
}

func locateDevice(ctx context.Context, adapter *bluetooth.Adapter, sp SearchParameters) (bluetooth.ScanResult, error) {
	if sp.MACAddress == "" && sp.Name == "" {
		return bluetooth.ScanResult{}, fmt.Errorf("cannot specify both MAC address and device name")
	}
	var (
		d        bluetooth.ScanResult
		canceled bool
	)
	err := adapter.Scan(func(a *bluetooth.Adapter, sr bluetooth.ScanResult) {
		if ctx.Err() != nil {
			slog.WarnContext(ctx, "Scan cancelled", "error", ctx.Err())
			canceled = true
			if err := a.StopScan(); err != nil {
				slog.ErrorContext(ctx, "Failed to stop scanning", "error", err)
			}
			return
		}
		if sr.LocalName() == sp.Name || sr.Address.String() == sp.MACAddress {
			slog.Info("Found printer", "name", sr.LocalName(), "address", sr.Address)
			d = sr
			if err := a.StopScan(); err != nil {
				slog.ErrorContext(ctx, "Failed to stop scanning", "error", err)
			}
			return
		}
	})
	if err != nil {
		return d, fmt.Errorf("failed to start scanning: %w", err)
	} else if canceled {
		return d, fmt.Errorf("scanning was cancelled: %w", ctx.Err())
	}
	slog.DebugContext(ctx, "Scanning complete", "device", d.Address, "name", d.LocalName())
	return d, nil
}

type txrx struct {
	tx bluetooth.DeviceCharacteristic
	rx bluetooth.DeviceCharacteristic
}

// locateCharacteristics discovers the TX and RX characteristics of the device.
func locateCharacteristics(device bluetooth.Device, tx string, rx string) (txrx, error) {
	var zero txrx
	services, err := device.DiscoverServices(nil) // all
	if err != nil {
		return zero, fmt.Errorf("failed to discover services: %w", err)
	}
	if len(services) == 0 {
		return zero, fmt.Errorf("no services found on device %s", device.Address)
	}
	slog.Debug("Discovered services", "services", services)
	var txrx txrx
	rxOK, txOK := false, false
	for _, service := range services {
		chars, err := service.DiscoverCharacteristics(nil) // all
		if err != nil {
			return zero, fmt.Errorf("failed to discover characteristics for service %s: %w", service.UUID().String(), err)
		}
		if len(chars) == 0 {
			continue
		}
		for _, char := range chars {
			slog.Debug("Discovered characteristic", "uuid", char.UUID().String())
			if char.UUID().String() == tx {
				slog.Debug("Found TX characteristic", "uuid", char.UUID().String())
				txrx.tx = char
				txOK = true
			} else if char.UUID().String() == rx {
				slog.Debug("Found RX characteristic", "uuid", char.UUID().String())
				txrx.rx = char
				rxOK = true
			}
			if txOK && rxOK {
				break
			}
		}
	}
	if !txOK || !rxOK {
		return txrx, fmt.Errorf("required characteristics not found: TX (%s) or RX (%s)", txChar, rxChar)
	}
	slog.Debug("Required characteristics found", "txChar", txChar, "rxChar", rxChar)

	// discover characteristics
	return txrx, nil

}
