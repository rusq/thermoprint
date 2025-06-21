package printers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

const (
	// DefaultPrintDelay is the default interval between sending packets to the printer
	DefaultPrintDelay = 7 * time.Millisecond
)

const (
	txChar = "0000ffe1-0000-1000-8000-00805f9b34fb" // TX Characteristic UUID
	rxChar = "0000ffe2-0000-1000-8000-00805f9b34fb" // RX Characteristic UUID
)

const (
	sendRetryDelay  = 10 * time.Millisecond  // Delay between sends to avoid overwhelming the printer
	maxRetries      = 3                      // Maximum retries for sending data
	cooldownDelay   = 100 * time.Millisecond // Cooldown period after certain notifications
	responseTimeout = 3 * time.Second        // Timeout for sending data and waiting for response
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
	printCancel context.CancelFunc

	responseMu    sync.Mutex
	waitingPrefix []byte
	responseCh    chan []byte

	options lxd02options
}

type lxd02options struct {
	energy        uint8         // 0-6
	printInterval time.Duration // Interval between sending data packets
}

type Option func(*lxd02options)

func WithEnergy(v uint8) Option {
	if v > 6 {
		v = 6 // Cap brightness to 6
	}
	return func(o *lxd02options) {
		o.energy = v
	}
}

func WithPrintInterval(d time.Duration) Option {
	if d <= 0 || 10*time.Second < d {
		d = DefaultPrintDelay // Default to 7ms if invalid
	}
	return func(o *lxd02options) {
		o.printInterval = d
	}
}

func NewLXD02(ctx context.Context, adapter *bluetooth.Adapter, sp SearchParameters, opt ...Option) (*LXD02, error) {
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

	var opts = lxd02options{
		energy:        2, // Default energy level
		printInterval: DefaultPrintDelay,
	}
	for _, o := range opt {
		o(&opts)
	}
	prn := &LXD02{
		dev:     device,
		tx:      txrx.tx,
		rx:      txrx.rx,
		options: opts,
	}

	notifyCh := make(chan lxd02notification, 10)
	if err := prn.rx.EnableNotifications(prn.notificationCallback(notifyCh)); err != nil {
		return nil, fmt.Errorf("failed to enable notifications on TX characteristic: %w", err)
	}
	slog.Debug("enabled notifications, starting worker")
	go prn.worker(ctx, notifyCh)

	slog.Info("Connected to printer", "address", device.Address, "mac", device.Address)
	return prn, nil
}

func (p *LXD02) notificationCallback(notifyCh chan<- lxd02notification) func(value []byte) {
	return func(value []byte) {
		if len(value) < 2 {
			slog.Warn("Received notification with insufficient length", "length", len(value))
			return
		}

		p.responseMu.Lock()
		if p.waitingPrefix != nil && bytes.HasPrefix(value, p.waitingPrefix) && p.responseCh != nil {
			// Copy to avoid race
			resp := make([]byte, len(value))
			copy(resp, value)
			select {
			case p.responseCh <- resp:
			default:
				slog.Warn("responseCh full or ignored")
			}
			p.waitingPrefix = nil
			p.responseCh = nil
			p.responseMu.Unlock()
			return
		}
		p.responseMu.Unlock()

		var prefix = notification(uint16(value[0])<<8 | uint16(value[1]))

		switch prefix {
		case ntStatus:
			notifyCh <- lxd02notification{prefix: ntStatus, data: value}
		case ntFinished:
			notifyCh <- lxd02notification{prefix: ntFinished, data: value}
		case ntRetransmit:
			notifyCh <- lxd02notification{prefix: ntRetransmit, data: value}
		case ntCooldown:
			time.Sleep(cooldownDelay) // Cooldown period
		case ntHold:
			notifyCh <- lxd02notification{prefix: ntHold, data: value}
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
			lg.DebugContext(ctx, "received notification")
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

func (p *LXD02) PrintImage(ctx context.Context, img image.Image) error {
	p.doneCh = make(chan struct{})
	p.eventCh = make(chan fsmEvent, 10)

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
	p.printCancel = cancel

	go func() {
		defer cancel()

		t := time.NewTicker(p.options.printInterval)
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
		p.eventCh <- fsmEvent{kind: eventNotificationFinished}
	}()
}

func (p *LXD02) sendInitSequence() {
	initSeq := [][]byte{
		{0x5a, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x5a, 0x0a, 0xB5, 0x7C, 0x4C, 0xB8, 0xAE, 0x70, 0x51, 0xE6, 0xD3, 0x06},
		{0x5a, 0x0b, 0x66, 0x3B, 0x62, 0x8C, 0x1A, 0x69, 0xBF, 0x54, 0x74, 0x4C},
		{0x5a, 0x0c, p.options.energy},
	}
	for _, cmd := range initSeq {
		expectPrefix := cmd[:2]
		resp, err := p.sendAndWait(cmd, expectPrefix, responseTimeout)
		if err != nil {
			p.eventCh <- fsmEvent{kind: eventError}
			return
		}
		slog.Debug("init ack", "prefix", fmt.Sprintf("% x", expectPrefix), "response", fmt.Sprintf("% x", resp))
	}
	p.eventCh <- fsmEvent{kind: eventInitComplete}
}

func extractRetryPacketIndex(data []byte) int {
	if len(data) < 4 {
		return 0
	}
	return int(data[2])<<8 | int(data[3])
}

func (p *LXD02) send(data []byte) error {

	for i := range maxRetries {
		_, err := p.tx.WriteWithoutResponse(data)
		if err == nil {
			return nil
		}
		slog.Warn("send failed, retrying", "attempt", i+1, "error", err)
		time.Sleep(sendRetryDelay)
	}
	return errors.New("BLE write failed after retries")
}

func (p *LXD02) sendAndWait(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
	p.responseMu.Lock()
	if p.responseCh != nil {
		p.responseMu.Unlock()
		return nil, errors.New("sendAndWait already in progress")
	}
	p.responseCh = make(chan []byte, 1)
	p.waitingPrefix = expectPrefix
	p.responseMu.Unlock()

	if _, err := p.tx.WriteWithoutResponse(data); err != nil {
		p.responseMu.Lock()
		p.responseCh = nil
		p.waitingPrefix = nil
		p.responseMu.Unlock()
		return nil, fmt.Errorf("send failed: %w", err)
	}

	select {
	case resp := <-p.responseCh:
		return resp, nil
	case <-time.After(timeout):
		p.responseMu.Lock()
		p.responseCh = nil
		p.waitingPrefix = nil
		p.responseMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to % X", expectPrefix)
	}
}
