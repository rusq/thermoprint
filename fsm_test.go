package thermoprint

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	fsmWaitTimeout  = 500 * time.Millisecond
	fsmBlockTimeout = 50 * time.Millisecond
)

type fsmSendAndWaitCall struct {
	data         []byte
	expectPrefix []byte
	timeout      time.Duration
}

func newFSMTestPrinter(packetCount int) *LXD02 {
	buffer := make([][]byte, packetCount)
	for i := range buffer {
		buffer[i] = []byte{byte(i)}
	}
	p := &LXD02{
		buffer:  buffer,
		state:   stateIdle,
		eventCh: make(chan fsmEvent, 20),
		doneCh:  make(chan error, 2),
		options: printOptions{
			printInterval: time.Millisecond,
		},
	}
	p.printFSM = p.newPrintFSM(stateIdle)
	return p
}

func setFSMState(p *LXD02, state printerState) {
	p.stateMu.Lock()
	p.state = state
	if p.printFSM == nil {
		p.printFSM = p.newPrintFSM(state)
	} else {
		p.printFSM.SetState(state.String())
	}
	p.stateMu.Unlock()
}

func waitForState(t *testing.T, p *LXD02, want printerState) {
	t.Helper()

	deadline := time.After(fsmWaitTimeout)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for {
		p.stateMu.Lock()
		got := p.state
		p.stateMu.Unlock()
		if got == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("timed out waiting for state %v; got %v", want, got)
		case <-tick.C:
		}
	}
}

func requireDone(t *testing.T, doneCh <-chan error) error {
	t.Helper()

	select {
	case err := <-doneCh:
		return err
	case <-time.After(fsmWaitTimeout):
		t.Fatal("timed out waiting for done signal")
		return nil
	}
}

func requireNoDone(t *testing.T, doneCh <-chan error) {
	t.Helper()

	select {
	case err := <-doneCh:
		t.Fatalf("unexpected done signal: %v", err)
	case <-time.After(fsmBlockTimeout):
	}
}

func requireEvent(t *testing.T, eventCh <-chan fsmEvent, want printerEvent) fsmEvent {
	t.Helper()

	select {
	case got := <-eventCh:
		if got.kind != want {
			t.Fatalf("event = %v, want %v", got.kind, want)
		}
		return got
	case <-time.After(fsmWaitTimeout):
		t.Fatalf("timed out waiting for event %v", want)
		return fsmEvent{}
	}
}

func requireNoFinish(t *testing.T, finished <-chan struct{}) {
	t.Helper()

	select {
	case <-finished:
		t.Fatal("operation completed unexpectedly")
	case <-time.After(fsmBlockTimeout):
	}
}

func TestFSM(t *testing.T) {
	t.Run("successful flow", func(t *testing.T) {
		p := newFSMTestPrinter(3)
		p.doneCh = make(chan error, 1)

		initCalled := make(chan struct{})
		releaseInit := make(chan struct{})
		p.initSequenceHook = func() {
			close(initCalled)
			<-releaseInit
			p.emitEvent(fsmEvent{kind: eventInitComplete})
		}

		streamStarts := make(chan int, 1)
		p.printBufferHook = func(start int) {
			streamStarts <- start
			p.emitEvent(fsmEvent{kind: eventPacketsSent})
		}

		var (
			callsMu sync.Mutex
			calls   []fsmSendAndWaitCall
		)
		p.sendAndWaitHook = func(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
			callsMu.Lock()
			calls = append(calls, fsmSendAndWaitCall{
				data:         append([]byte(nil), data...),
				expectPrefix: append([]byte(nil), expectPrefix...),
				timeout:      timeout,
			})
			callsMu.Unlock()
			return append([]byte(nil), expectPrefix...), nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.runFSM(ctx)

		p.emitEvent(fsmEvent{kind: eventStart})

		select {
		case <-initCalled:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("init sequence was not started")
		}
		waitForState(t, p, stateInitializing)

		close(releaseInit)
		select {
		case got := <-streamStarts:
			if got != 0 {
				t.Fatalf("print stream started at packet %d, want 0", got)
			}
		case <-time.After(fsmWaitTimeout):
			t.Fatal("print stream was not started")
		}
		waitForState(t, p, stateWaitingRetry)

		callsMu.Lock()
		if len(calls) != 1 {
			t.Fatalf("sendAndWait calls after begin = %d, want 1", len(calls))
		}
		if got, want := calls[0].data, []byte{0x5a, 0x04, 0x00, 0x03, 0x00, 0x00}; !bytes.Equal(got, want) {
			t.Fatalf("begin command = % x, want % x", got, want)
		}
		callsMu.Unlock()

		p.emitEvent(fsmEvent{kind: eventNotificationFinished})
		if err := requireDone(t, p.doneCh); err != nil {
			t.Fatalf("done error = %v, want nil", err)
		}
		waitForState(t, p, stateCompleted)

		callsMu.Lock()
		defer callsMu.Unlock()
		if len(calls) != 2 {
			t.Fatalf("sendAndWait calls after final = %d, want 2", len(calls))
		}
		if got, want := calls[1].data, []byte{0x5a, 0x04, 0x00, 0x03, 0x01, 0x00}; !bytes.Equal(got, want) {
			t.Fatalf("final command = % x, want % x", got, want)
		}
	})

	t.Run("init failure transitions and returns error", func(t *testing.T) {
		wantErr := errors.New("init failed")
		p := newFSMTestPrinter(1)
		p.initSequenceHook = func() {
			p.emitEvent(fsmEvent{kind: eventError, err: wantErr})
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.runFSM(ctx)

		p.emitEvent(fsmEvent{kind: eventStart})
		if err := requireDone(t, p.doneCh); !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("begin command failure transitions and returns error", func(t *testing.T) {
		wantErr := errors.New("begin failed")
		p := newFSMTestPrinter(1)
		p.initSequenceHook = func() {
			p.emitEvent(fsmEvent{kind: eventInitComplete})
		}
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, wantErr
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.runFSM(ctx)

		p.emitEvent(fsmEvent{kind: eventStart})
		err := requireDone(t, p.doneCh)
		if !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("final command failure transitions and returns error", func(t *testing.T) {
		wantErr := errors.New("final failed")
		p := newFSMTestPrinter(1)
		setFSMState(p, stateWaitingRetry)
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, wantErr
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.runFSM(ctx)

		p.emitEvent(fsmEvent{kind: eventNotificationFinished})
		err := requireDone(t, p.doneCh)
		if !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("packet send failure emits error", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		wantErr := errors.New("packet failed")
		p.sendPacketHook = func([]byte) error {
			return wantErr
		}

		p.printBuffer(0)
		got := requireEvent(t, p.eventCh, eventError)
		if !errors.Is(got.err, wantErr) {
			t.Fatalf("event error = %v, want %v", got.err, wantErr)
		}
	})

	t.Run("context cancellation returns cancellation error", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.initSequenceHook = func() {
			p.emitEvent(fsmEvent{kind: eventInitComplete})
		}
		p.sendAndWaitHook = func(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
			return append([]byte(nil), expectPrefix...), nil
		}
		streamStarted := make(chan struct{})
		p.printBufferHook = func(int) {
			close(streamStarted)
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- p.printPackets(ctx, p.buffer)
		}()

		select {
		case <-streamStarted:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("print stream was not started")
		}
		cancel()

		select {
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("printPackets error = %v, want context.Canceled", err)
			}
		case <-time.After(fsmWaitTimeout):
			t.Fatal("printPackets did not return after cancellation")
		}
	})

	t.Run("hold during printing cancels stream and pauses", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		setFSMState(p, statePrinting)
		cancelled := make(chan struct{})
		var once sync.Once
		p.printCancel = func() {
			once.Do(func() { close(cancelled) })
		}

		p.transition(eventNotificationHold, nil)

		select {
		case <-cancelled:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("printCancel was not called")
		}
		waitForState(t, p, statePaused)
	})

	for _, tc := range []struct {
		name  string
		state printerState
		data  []byte
		want  int
	}{
		{
			name:  "paused retransmit resumes from requested packet",
			state: statePaused,
			data:  []byte{0x5a, 0x05, 0x01, 0x02},
			want:  258,
		},
		{
			name:  "waiting retry retransmit resumes from requested packet",
			state: stateWaitingRetry,
			data:  []byte{0x5a, 0x05, 0x00, 0x07},
			want:  7,
		},
		{
			name:  "malformed retransmit data starts from zero",
			state: statePaused,
			data:  []byte{0x5a, 0x05, 0x01},
			want:  0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newFSMTestPrinter(10)
			setFSMState(p, tc.state)
			starts := make(chan int, 1)
			p.printBufferHook = func(start int) {
				starts <- start
			}

			p.transition(eventNotificationRetransmit, tc.data)

			waitForState(t, p, statePrinting)
			select {
			case got := <-starts:
				if got != tc.want {
					t.Fatalf("stream start = %d, want %d", got, tc.want)
				}
			case <-time.After(fsmWaitTimeout):
				t.Fatal("print stream was not restarted")
			}
		})
	}

	t.Run("duplicate error and cancel complete once without blocking", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		setFSMState(p, statePrinting)
		p.doneCh = make(chan error, 2)

		p.transition(eventError, nil)
		p.transition(eventCancel, nil)

		if err := requireDone(t, p.doneCh); !errors.Is(err, errPrintFailed) {
			t.Fatalf("done error = %v, want %v", err, errPrintFailed)
		}
		requireNoDone(t, p.doneCh)
		waitForState(t, p, stateFailed)
	})

	t.Run("cancel after failed does not block without a done receiver", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		setFSMState(p, stateFailed)
		p.doneCh = make(chan error)
		finished := make(chan struct{})

		go func() {
			p.transition(eventCancel, nil)
			close(finished)
		}()

		select {
		case <-finished:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("transition blocked without a done receiver")
		}
	})
}

func TestNotificationWorker(t *testing.T) {
	for _, tc := range []struct {
		name string
		ntf  lxd02notification
		want printerEvent
	}{
		{
			name: "no paper status emits error",
			ntf:  lxd02notification{prefix: ntStatus, data: []byte{0x5a, 0x02, 50, 1, 0, 0}},
			want: eventError,
		},
		{
			name: "hold notification emits hold event",
			ntf:  lxd02notification{prefix: ntHold, data: []byte{0x5a, 0x08}},
			want: eventNotificationHold,
		},
		{
			name: "retransmit notification emits retransmit event with data",
			ntf:  lxd02notification{prefix: ntRetransmit, data: []byte{0x5a, 0x05, 0x00, 0x09}},
			want: eventNotificationRetransmit,
		},
		{
			name: "finished notification emits finished event",
			ntf:  lxd02notification{prefix: ntFinished, data: []byte{0x5a, 0x06}},
			want: eventNotificationFinished,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newFSMTestPrinter(1)
			notifyCh := make(chan lxd02notification, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go p.worker(ctx, notifyCh)

			notifyCh <- tc.ntf
			got := requireEvent(t, p.eventCh, tc.want)
			if tc.want == eventNotificationRetransmit && !bytes.Equal(got.data, tc.ntf.data) {
				t.Fatalf("retransmit data = % x, want % x", got.data, tc.ntf.data)
			}
		})
	}

	t.Run("no active event receiver does not block worker", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.eventCh = nil
		notifyCh := make(chan lxd02notification)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.worker(ctx, notifyCh)

		for _, ntf := range []lxd02notification{
			{prefix: ntHold, data: []byte{0x5a, 0x08}},
			{prefix: ntFinished, data: []byte{0x5a, 0x06}},
		} {
			sent := make(chan struct{})
			go func() {
				notifyCh <- ntf
				close(sent)
			}()
			select {
			case <-sent:
			case <-time.After(fsmWaitTimeout):
				t.Fatal("worker blocked while no print was active")
			}
		}
	})
}
