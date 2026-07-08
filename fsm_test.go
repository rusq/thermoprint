package thermoprint

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

const (
	fsmWaitTimeout  = 500 * time.Millisecond
	fsmBlockTimeout = 50 * time.Millisecond

	nilEventChWorkerHelperEnv = "THERMOPRINT_NIL_EVENTCH_WORKER_HELPER"
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
	return &LXD02{
		buffer:  buffer,
		state:   stateIdle,
		eventCh: make(chan fsmEvent, 20),
		doneCh:  make(chan struct{}, 2),
		options: printOptions{
			printInterval: time.Millisecond,
		},
	}
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

func requireDone(t *testing.T, doneCh <-chan struct{}) {
	t.Helper()

	select {
	case <-doneCh:
	case <-time.After(fsmWaitTimeout):
		t.Fatal("timed out waiting for done signal")
	}
}

func requireNoDone(t *testing.T, doneCh <-chan struct{}) {
	t.Helper()

	select {
	case <-doneCh:
		t.Fatal("unexpected done signal")
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

func TestFSMCurrent(t *testing.T) {
	t.Run("successful flow", func(t *testing.T) {
		p := newFSMTestPrinter(3)
		p.doneCh = make(chan struct{})

		initCalled := make(chan struct{})
		releaseInit := make(chan struct{})
		p.initSequenceHook = func() {
			close(initCalled)
			<-releaseInit
			p.eventCh <- fsmEvent{kind: eventInitComplete}
		}

		streamStarts := make(chan int, 1)
		p.printBufferHook = func(start int) {
			streamStarts <- start
			p.eventCh <- fsmEvent{kind: eventNotificationFinished}
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

		p.eventCh <- fsmEvent{kind: eventStart}

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
		if got, want := calls[0].data, []byte{0x5a, 0x04, 0x00, 0x03, 0x00, 0x00}; !bytesEqual(got, want) {
			t.Fatalf("begin command = % x, want % x", got, want)
		}
		callsMu.Unlock()

		p.eventCh <- fsmEvent{kind: eventNotificationFinished}
		requireDone(t, p.doneCh)
		waitForState(t, p, stateCompleted)

		callsMu.Lock()
		defer callsMu.Unlock()
		if len(calls) != 2 {
			t.Fatalf("sendAndWait calls after final = %d, want 2", len(calls))
		}
		if got, want := calls[1].data, []byte{0x5a, 0x04, 0x00, 0x03, 0x01, 0x00}; !bytesEqual(got, want) {
			t.Fatalf("final command = % x, want % x", got, want)
		}
	})

	t.Run("init failure transitions and signals failed", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.initSequenceHook = func() {
			p.eventCh <- fsmEvent{kind: eventError}
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.runFSM(ctx)

		p.eventCh <- fsmEvent{kind: eventStart}
		requireDone(t, p.doneCh)
		waitForState(t, p, stateFailed)
	})

	t.Run("begin command failure transitions and signals failed", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.initSequenceHook = func() {
			p.eventCh <- fsmEvent{kind: eventInitComplete}
		}
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, errors.New("begin failed")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.runFSM(ctx)

		p.eventCh <- fsmEvent{kind: eventStart}
		requireDone(t, p.doneCh)
		waitForState(t, p, stateFailed)
	})

	t.Run("final command failure emits error after completed without done", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.state = stateWaitingRetry
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, errors.New("final failed")
		}

		p.transition(eventNotificationFinished, nil)

		waitForState(t, p, stateCompleted)
		requireEvent(t, p.eventCh, eventError)
		requireNoDone(t, p.doneCh)
	})

	t.Run("packet send failure emits error", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.sendPacketHook = func([]byte) error {
			return errors.New("packet failed")
		}

		p.printBuffer(0)
		requireEvent(t, p.eventCh, eventError)
	})

	t.Run("hold during printing cancels stream and pauses", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.state = statePrinting
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
			p.state = tc.state
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

	t.Run("context cancellation transitions and signals failed", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.state = statePrinting

		ctx, cancel := context.WithCancel(context.Background())
		go p.runFSM(ctx)
		cancel()

		requireDone(t, p.doneCh)
		waitForState(t, p, stateFailed)
	})

	t.Run("error while printing blocks without a second done receiver", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.state = statePrinting
		p.doneCh = make(chan struct{})
		finished := make(chan struct{})

		go func() {
			p.transition(eventError, nil)
			close(finished)
		}()

		requireDone(t, p.doneCh)
		requireNoFinish(t, finished)

		requireDone(t, p.doneCh)
		select {
		case <-finished:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("transition did not finish after second done receiver")
		}
	})

	t.Run("cancel after failed blocks without a done receiver", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.state = stateFailed
		p.doneCh = make(chan struct{})
		finished := make(chan struct{})

		go func() {
			p.transition(eventCancel, nil)
			close(finished)
		}()

		requireNoFinish(t, finished)
		requireDone(t, p.doneCh)
		select {
		case <-finished:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("transition did not finish after done receiver")
		}
	})
}

func TestNotificationWorkerCurrent(t *testing.T) {
	if os.Getenv(nilEventChWorkerHelperEnv) == "1" {
		runNilEventChWorkerBlockHelper(t)
		return
	}

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
			if tc.want == eventNotificationRetransmit && !bytesEqual(got.data, tc.ntf.data) {
				t.Fatalf("retransmit data = % x, want % x", got.data, tc.ntf.data)
			}
		})
	}

	t.Run("no active event receiver blocks worker", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.eventCh = make(chan fsmEvent)
		notifyCh := make(chan lxd02notification)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.worker(ctx, notifyCh)

		firstSent := make(chan struct{})
		go func() {
			notifyCh <- lxd02notification{prefix: ntHold, data: []byte{0x5a, 0x08}}
			close(firstSent)
		}()
		select {
		case <-firstSent:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("worker did not receive first notification")
		}

		secondSent := make(chan struct{})
		go func() {
			notifyCh <- lxd02notification{prefix: ntFinished, data: []byte{0x5a, 0x06}}
			close(secondSent)
		}()
		requireNoFinish(t, secondSent)

		requireEvent(t, p.eventCh, eventNotificationHold)
		select {
		case <-secondSent:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("worker did not receive second notification after event receiver attached")
		}
		requireEvent(t, p.eventCh, eventNotificationFinished)
	})

	t.Run("nil event channel blocks worker", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), fsmWaitTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNotificationWorkerCurrent$")
		cmd.Env = append(os.Environ(), nilEventChWorkerHelperEnv+"=1")
		err := cmd.Run()
		if ctx.Err() == context.DeadlineExceeded {
			return
		}
		if err != nil {
			t.Fatalf("helper exited before documenting nil event channel block: %v", err)
		}
		t.Fatal("helper exited successfully; want worker blocked on nil event channel")
	})
}

func runNilEventChWorkerBlockHelper(t *testing.T) {
	p := newFSMTestPrinter(1)
	p.eventCh = nil
	notifyCh := make(chan lxd02notification)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.worker(ctx, notifyCh)

	firstSent := make(chan struct{})
	go func() {
		notifyCh <- lxd02notification{prefix: ntHold, data: []byte{0x5a, 0x08}}
		close(firstSent)
	}()
	select {
	case <-firstSent:
	case <-time.After(fsmWaitTimeout):
		t.Fatal("worker did not receive first notification")
	}

	secondSent := make(chan struct{})
	go func() {
		notifyCh <- lxd02notification{prefix: ntFinished, data: []byte{0x5a, 0x06}}
		close(secondSent)
	}()
	requireNoFinish(t, secondSent)

	select {}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
