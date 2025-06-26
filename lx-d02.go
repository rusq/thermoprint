// Package thermoprint implements printing on a LX-D02 (Dolebo) bluetooth thermal
// printer.
package thermoprint

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/image/font"
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

// LXD02 represents a LX-D02 printer.  Instance is not safe for concurrent use.
// Zero value is unusable, initialise with [NewLXD02]
type LXD02 struct {
	dev        bluetooth.Device
	tx         bluetooth.DeviceCharacteristic
	rx         bluetooth.DeviceCharacteristic
	buffer     [][]byte
	rasteriser Rasteriser // Interface for rasterizing images

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

var LXD02Rasteriser = &Raster{
	Width:          384, // 48 bytes
	Dpi:            203, // 203 DPI
	LinesPerPacket: 2,   // 2 lines per packet
	PrefixFunc: func(packetIndex int) []byte {
		m := byte((packetIndex >> 8) & 0xFF)
		n := byte(packetIndex & 0xFF)
		return []byte{0x55, m, n} // 55 m n
	},
	Terminator: 0x00,             // 00
	Threshold:  DefaultThreshold, // default threshold for dark pixels
	DitherFunc: ditherimg,        // default dither function
}

type lxd02options struct {
	energy        uint8         // 0-6
	printInterval time.Duration // Interval between sending data packets
	crop          bool          // crop instead of scaling
	dithername    string        // Name of the dither function to use
	dryrun        bool          // If true, don't actually send data to the printer, output raster images
	gamma         float64       // gamma
	autoDither    bool
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

// WithGamma allows to specify rasteriser gamma correction value.
// [DefaultGamma] value or 0.0 means use default for the selected dither
// function.
func WithGamma(gamma float64) Option {
	return func(l *lxd02options) {
		if gamma > 0.0 {
			l.gamma = gamma
		}
	}
}

func WithCrop(crop bool) Option {
	return func(o *lxd02options) {
		o.crop = crop
	}
}

func WithDither(name string) Option {
	return func(o *lxd02options) {
		_, ok := ditherFunctions[name]
		if !ok {
			return
		}
		o.dithername = name
	}
}

func WithDryRun(dryrun bool) Option {
	return func(o *lxd02options) {
		o.dryrun = dryrun
	}
}

func WithAutoDither(isEnabled bool) Option {
	return func(o *lxd02options) {
		o.autoDither = isEnabled
	}
}

func NewLXD02(ctx context.Context, adapter *bluetooth.Adapter, sp SearchParameters, opt ...Option) (*LXD02, error) {
	var opts = lxd02options{
		energy:        2, // Default energy level
		printInterval: DefaultPrintDelay,
	}
	for _, o := range opt {
		o(&opts)
	}
	prn := &LXD02{
		options:    opts,
		rasteriser: LXD02Rasteriser, // Default rasteriser for LXD02
	}
	if !opts.dryrun {
		device, err := connectWithRetries(ctx, adapter, sp, 5)
		if err != nil {
			return nil, err
		}
		prn.dev = device

		txrx, err := locateCharacteristics(device, txChar, rxChar)
		if err != nil {
			return nil, fmt.Errorf("failed to locate services: %w", err)
		}
		prn.tx = txrx.tx
		prn.rx = txrx.rx
		slog.Info("Connected to printer", "address", device.Address, "mac", device.Address)

		notifyCh := make(chan lxd02notification, 10)
		if err := prn.rx.EnableNotifications(prn.notificationCallback(notifyCh)); err != nil {
			return nil, fmt.Errorf("failed to enable notifications on TX characteristic: %w", err)
		}
		slog.Debug("enabled notifications, starting worker")
		go prn.worker(ctx, notifyCh)
	}

	if opts.dithername != "" {
		ditherFunc, ok := ditherFunctions[opts.dithername]
		if !ok {
			return nil, fmt.Errorf("unknown dither function: %s", opts.dithername)
		}
		prn.rasteriser.SetDitherFunc(ditherFunc)
		slog.Debug("Using dither function", "name", opts.dithername)
	}

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
			slog.Debug("Worker context done, exiting")
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
				if st.BatteryLevel < 10.0 {
					slog.WarnContext(ctx, "battery level critical")
				}
				if st.NoPaper {
					slog.ErrorContext(ctx, "no paper")
					p.eventCh <- fsmEvent{kind: eventError}
				}
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
	if p.options.dryrun {
		return nil
	}
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

// dry run file names
const (
	drRasteriseFile = "preview_rasterised.png"
	drTextFile      = "preview_text_image.png"
	drPatternFile   = "preview_pattern_image.png"
)

// PrintImage prints an image on the printer.  If dry run is enabled, it saves
// the preview file to disk and exits.
func (p *LXD02) PrintImage(ctx context.Context, img image.Image) error {
	bmp := p.rasteriser.ResizeAndDither(img, p.options.gamma, p.options.autoDither)
	if p.options.dryrun {
		// DRY RUN terminates here.
		debugSaveImage(bmp, drRasteriseFile)
		return nil
	}

	packets, err := p.rasteriser.Serialise(bmp)
	if err != nil {
		return err
	}

	return p.printPackets(ctx, packets)
}

func (p *LXD02) PrintRAW(ctx context.Context, data [][]byte) error {
	if len(data) == 0 {
		return errors.New("empty raw data")
	}

	packets, err := p.rasteriser.Enumerate(data)
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "packet stat", "len", len(packets))

	return p.printPackets(ctx, packets)
}

// printPackets is the low level routine that starts the FSM and sends the
// encoded image data to the printer.
func (p *LXD02) printPackets(ctx context.Context, packets [][]byte) error {
	p.doneCh = make(chan struct{})
	p.eventCh = make(chan fsmEvent, 10)
	p.loadBuffer(packets)

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

func (p *LXD02) PrintTextTTF(ctx context.Context, text string, face font.Face) error {
	// rasterizeText
	img, err := renderTTF(text, face, p.rasteriser.LineWidth())
	if err != nil {
		return fmt.Errorf("failed to render TTF text: %w", err)
	}

	if p.options.dryrun {
		debugSaveImage(img, drTextFile) //
	}
	return p.PrintImage(ctx, img)
}

func debugSaveImage(img image.Image, filename string) {
	f, err := os.Create(filename)
	if err != nil {
		slog.Error("Failed to create debug image file", "filename", filename, "error", err)
		return
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		slog.Error("Failed to encode debug image", "filename", filename, "error", err)
	}
	slog.Debug("Debug image saved", "filename", filename)
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
		slog.Debug("Sending data", "state", p.state, "attempt", i+1, "data", fmt.Sprintf("% X", data))
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

	slog.Debug("Sending data", "state", p.state, "data", fmt.Sprintf("% X", data), "expectPrefix", fmt.Sprintf("% X", expectPrefix))

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

// Width returns the maximum width of the print output in pixels.
func (p *LXD02) Width() int {
	return p.rasteriser.LineWidth()
}

func (p *LXD02) PrintPattern(ctx context.Context, pattern string) error {
	if imgFn, ok := TestImagePatterns[pattern]; ok {
		return p.printImagePattern(ctx, imgFn)
	}
	if bufPatFn, ok := TestBufferPatterns[pattern]; ok {
		return p.printBufferPattern(ctx, bufPatFn)
	}
	return fmt.Errorf("unknown test pattern: %s", pattern)
}

func (p *LXD02) printImagePattern(ctx context.Context, imgFn func(int) image.Image) error {
	img := imgFn(p.rasteriser.LineWidth())
	if img == nil {
		return errors.New("test image pattern returned nil image")
	}

	if p.options.dryrun {
		debugSaveImage(img, drPatternFile) // Save debug image
	}
	return p.PrintImage(ctx, img)
}

func (p *LXD02) printBufferPattern(ctx context.Context, bufPatFn func(int) [][]byte) error {
	data := bufPatFn(p.rasteriser.LineWidth())
	if data == nil {
		return errors.New("test buffer pattern returned no data")
	}
	if p.options.dryrun {
		return errors.New("buffer patterns do not support dry run")
	}
	return p.PrintRAW(ctx, data)
}
