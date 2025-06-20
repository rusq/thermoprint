package printers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

const (
	txChar = "0000ffe1-0000-1000-8000-00805f9b34fb" // TX Characteristic UUID
	rxChar = "0000ffe2-0000-1000-8000-00805f9b34fb" // RX Characteristic UUID
)

type LXD02 struct {
	dev    bluetooth.Device
	tx     bluetooth.DeviceCharacteristic
	rx     bluetooth.DeviceCharacteristic
	buffer [][]byte

	stateMu     sync.Mutex
	state       printerState
	eventCh     chan fsmEvent
	doneCh      chan struct{}
	cancelCtx   context.CancelFunc
	printCtx    context.Context
	printCancel context.CancelFunc
}

func NewLXD02(ctx context.Context, adapter *bluetooth.Adapter, sp SearchParameters) (*LXD02, error) {
	foundDevice, err := LocateDevice(ctx, adapter, sp)
	if err != nil {
		return nil, fmt.Errorf("failed to locate device: %w", err)
	}

	device, err := adapter.Connect(foundDevice.Address, bluetooth.ConnectionParams{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to device: %w", err)
	}
	txrx, err := locateCharacteristics(device, txChar, rxChar)
	if err != nil {
		return nil, fmt.Errorf("failed to locate services: %w", err)
	}

	prn := &LXD02{
		dev: device,
		tx:  txrx.tx,
		rx:  txrx.rx,
	}

	cmdCh := make(chan lxd02notification, 10)
	if err := prn.rx.EnableNotifications(prn.notificationCallback(cmdCh)); err != nil {
		return nil, fmt.Errorf("failed to enable notifications on TX characteristic: %w", err)
	}
	slog.Debug("enabled notifications, starting worker")
	go prn.worker(ctx, cmdCh)

	slog.Info("Connected to printer", "address", device.Address, "mac", device.Address)
	return prn, nil
}

var lxd02endian = binary.BigEndian

func (p *LXD02) notificationCallback(cmdCh chan<- lxd02notification) func(value []byte) {
	return func(value []byte) {
		if len(value) < 2 {
			slog.Warn("Received notification with insufficient length", "length", len(value))
			return
		}

		var prefix uint16
		if _, err := binary.Decode(value[:2], lxd02endian, &prefix); err != nil {
			slog.Error("Failed to decode prefix", "error", err)
			return
		}

		switch notification(prefix) {
		case ntStatus:
			cmdCh <- lxd02notification{prefix: ntStatus, data: value}
		default:
			slog.Warn("Received unknown notification", "value", fmt.Sprintf("% x", value))
		}
	}
	// Handle the received notification value here
}

type lxd02status struct {
	BatteryLevel uint8
	NoPaper      bool
	Charging     bool
	Charged      bool
}

var (
	prefixStatus = []byte{0x5a, 0x02} // Prefix for status messages
)

func (s lxd02status) String() string {
	return fmt.Sprintf("Battery Level: %d%%, No Paper: %t, Charging: %t, Charged: %t",
		s.BatteryLevel, s.NoPaper, s.Charging, s.Charged)
}

func parseStatus(data []byte) (lxd02status, error) {
	if !bytes.HasPrefix(data, []byte{0x5a, 0x02}) || len(data) < 6 {
		return lxd02status{}, fmt.Errorf("invalid status data prefix or length: %x", data[:2])
	}
	payload := data[2:]
	status := lxd02status{
		BatteryLevel: payload[0],
		NoPaper:      payload[1] != 0,
		Charging:     payload[2] == 1,
		Charged:      payload[2] == 2,
	}
	return status, nil
}

type lxd02notification struct {
	prefix notification
	data   []byte
}

type notification uint16

const (
	ntStatus     notification = 0x5A02
	ntRetransmit notification = 0x5A05
	ntFinished   notification = 0x5A06
	ntCooldown   notification = 0x5A07
	ntHold       notification = 0x5A08 // Hold the job, wait for next notification
)

func (i notification) String() string {
	return fmt.Sprintf("%x", int(i))
}

func (p *LXD02) worker(ctx context.Context, notifyCh <-chan lxd02notification) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("Worker context done, exiting")
			return
		case ntf := <-notifyCh:
			lg := slog.With("instruction", ntf.prefix, "data", fmt.Sprintf("% x", ntf.data))
			lg.DebugContext(ctx, "command")
			switch ntf.prefix {
			case ntStatus:
				st, err := parseStatus(ntf.data)
				if err != nil {
					slog.Error("Failed to parse status", "error", err)
					continue
				}
				slog.InfoContext(ctx, "status", "status", st)
			case ntHold:
				p.eventCh <- fsmEvent{kind: eventNotificationHold}
			case ntRetransmit:
				p.eventCh <- fsmEvent{kind: eventNotificationRetransmit, data: ntf.data}
			case ntFinished:
				p.eventCh <- fsmEvent{kind: eventNotificationFinished}
			default:
				lg.WarnContext(ctx, "unsupported command")
			}
		}
	}
}

func (p *LXD02) Disconnect() error {
	if err := p.rx.EnableNotifications(func([]byte) {}); err != nil { // noop callback
		slog.Warn("failed to disable notifications, never mind, let's continue", "error", err)
	}
	if err := p.dev.Disconnect(); err != nil {
		return fmt.Errorf("failed to disconnect from printer: %w", err)
	}
	slog.Info("Disconnected from printer", "address", p.dev.Address)
	return nil
}

func (p *LXD02) loadBuffer(data [][]byte) {
	p.buffer = make([][]byte, len(data))
	copy(p.buffer, data)
}

func (p *LXD02) PrintImage(img image.Image) error {
	p.doneCh = make(chan struct{})
	p.eventCh = make(chan fsmEvent, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	p.cancelCtx = cancel
	defer cancel()

	p.loadBuffer(rasterizeImage(img))
	go p.runFSM(ctx)

	p.eventCh <- fsmEvent{kind: eventStart}

	select {
	case <-p.doneCh:
		slog.Info("print completed successfully")
		return nil
	case <-ctx.Done():
		p.eventCh <- fsmEvent{kind: eventCancel}
		return errors.New("print job timed out or cancelled")
	}
}

var errBufferEmpty = errors.New("buffer is empty")

// printBuffer sends the buffer to printer starting from
// packet n.
func (p *LXD02) printBuffer(start int) {
	if len(p.buffer) == 0 || start >= len(p.buffer) {
		p.eventCh <- fsmEvent{kind: eventError}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.printCtx = ctx
	p.printCancel = cancel

	go func() {
		defer cancel()

		t := time.NewTicker(30 * time.Millisecond)
		defer t.Stop()

		for i := start; i < len(p.buffer); i++ {
			select {
			case <-ctx.Done():
				slog.Debug("Print buffer cancelled at packet", "packet", i)
				return
			case <-t.C:
				err := p.send(p.buffer[i])
				if err != nil {
					slog.Error("Failed to send packet", "packet", i, "error", err)
					p.eventCh <- fsmEvent{kind: eventError}
					return
				}
			}
		}

		slog.Info("All packets sent, waiting for printer to complete (5a06)")
		p.stateMu.Lock()
		p.state = stateWaitingRetry // intermediate state while waiting
		p.stateMu.Unlock()
	}()
}

func (p *LXD02) sendInitSequence() {
	initSeq := [][]byte{
		{0x5a, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x5a, 0x0a, 0xB5, 0x7C, 0x4C, 0xB8, 0xAE, 0x70, 0x51, 0xE6, 0xD3, 0x06},
		{0x5a, 0x0b, 0x66, 0x3B, 0x62, 0x8C, 0x1A, 0x69, 0xBF, 0x54, 0x74, 0x4C},
		{0x5a, 0x0c, 0x02}, // TODO: function
	}
	for _, cmd := range initSeq {
		if err := p.send(cmd); err != nil {
			p.eventCh <- fsmEvent{kind: eventError}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.eventCh <- fsmEvent{kind: eventNotificationStatus}
}

func extractRetryPacketIndex(data []byte) int {
	if len(data) < 4 {
		return 0
	}
	return int(data[2])<<8 | int(data[3])
}

func (p *LXD02) send(data []byte) error {
	const maxRetries = 3

	for i := range maxRetries {
		_, err := p.tx.WriteWithoutResponse(data)
		if err == nil {
			return nil
		}
		slog.Warn("send failed, retrying", "attempt", i+1, "error", err)
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("BLE write failed after retries")
}
