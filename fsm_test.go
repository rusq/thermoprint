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

type fsmStreamStart struct {
	start    int
	streamID uint64
}

func newFSMTestPrinter(packetCount int) *LXD02 {
	buffer := make([][]byte, packetCount)
	for i := range buffer {
		buffer[i] = []byte{byte(i)}
	}
	p := &LXD02{
		buffer: buffer,
		state:  stateIdle,
		options: printOptions{
			printInterval: time.Millisecond,
		},
	}
	job := p.newPrintJob(context.Background())
	p.activeJob = job
	return p
}

func activeTestJob(t *testing.T, p *LXD02) *printJob {
	t.Helper()

	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.activeJob == nil {
		t.Fatal("active job is nil")
	}
	return p.activeJob
}

func setFSMState(p *LXD02, state printerState) {
	p.stateMu.Lock()
	p.state = state
	p.activeJob.fsm.SetState(state.String())
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

func requireStreamStart(t *testing.T, starts <-chan fsmStreamStart) fsmStreamStart {
	t.Helper()

	select {
	case got := <-starts:
		return got
	case <-time.After(fsmWaitTimeout):
		t.Fatal("timed out waiting for print stream start")
		return fsmStreamStart{}
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
		job := activeTestJob(t, p)

		initCalled := make(chan struct{})
		releaseInit := make(chan struct{})
		p.initSequenceHook = func(job *printJob) {
			close(initCalled)
			<-releaseInit
			p.dispatchJobEvent(job, fsmEvent{kind: eventInitComplete})
		}

		streamStarts := make(chan int, 1)
		p.printBufferHook = func(job *printJob, start int, streamID uint64) {
			streamStarts <- start
			p.dispatchJobEvent(job, fsmEvent{kind: eventPacketsSent, streamID: streamID})
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

		go p.runFSM(job)

		p.dispatchJobEvent(job, fsmEvent{kind: eventStart})

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

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationFinished})
		if err := requireDone(t, job.doneCh); err != nil {
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
		job := activeTestJob(t, p)
		p.initSequenceHook = func(job *printJob) {
			p.dispatchJobEvent(job, fsmEvent{kind: eventError, err: wantErr})
		}

		go p.runFSM(job)

		p.dispatchJobEvent(job, fsmEvent{kind: eventStart})
		if err := requireDone(t, job.doneCh); !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("begin command failure transitions and returns error", func(t *testing.T) {
		wantErr := errors.New("begin failed")
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		p.initSequenceHook = func(job *printJob) {
			p.dispatchJobEvent(job, fsmEvent{kind: eventInitComplete})
		}
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, wantErr
		}

		go p.runFSM(job)

		p.dispatchJobEvent(job, fsmEvent{kind: eventStart})
		err := requireDone(t, job.doneCh)
		if !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("final command failure transitions and returns error", func(t *testing.T) {
		wantErr := errors.New("final failed")
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		setFSMState(p, stateWaitingRetry)
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, wantErr
		}

		go p.runFSM(job)

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationFinished})
		err := requireDone(t, job.doneCh)
		if !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("packet send failure emits error", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		wantErr := errors.New("packet failed")
		p.sendPacketHook = func([]byte) error {
			return wantErr
		}

		p.startPrintBuffer(job, 0)
		err := requireDone(t, job.doneCh)
		if !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
	})

	t.Run("context cancellation returns cancellation error", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.initSequenceHook = func(job *printJob) {
			p.dispatchJobEvent(job, fsmEvent{kind: eventInitComplete})
		}
		p.sendAndWaitHook = func(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
			return append([]byte(nil), expectPrefix...), nil
		}
		streamStarted := make(chan struct{})
		p.printBufferHook = func(*printJob, int, uint64) {
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

	t.Run("hold during printing does not stop stream", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		setFSMState(p, statePrinting)
		p.stateMu.Lock()
		job.printSeq = 7
		job.printStream = 7
		p.stateMu.Unlock()
		cancelled := make(chan struct{})
		job.printCancel = func() {
			close(cancelled)
		}

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationHold})

		waitForState(t, p, statePaused)
		requireNoFinish(t, cancelled)
	})

	t.Run("packet completion after hold waits for printer completion", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		setFSMState(p, statePrinting)
		p.stateMu.Lock()
		job.printSeq = 7
		job.printStream = 7
		p.stateMu.Unlock()

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationHold})
		waitForState(t, p, statePaused)

		if ok := p.dispatchJobEvent(job, fsmEvent{kind: eventPacketsSent, streamID: 7}); !ok {
			t.Fatal("packet completion after hold was ignored")
		}
		waitForState(t, p, stateWaitingRetry)
	})

	t.Run("packet completion before paused retransmit is ignored", func(t *testing.T) {
		p := newFSMTestPrinter(10)
		job := activeTestJob(t, p)
		setFSMState(p, statePrinting)
		p.stateMu.Lock()
		job.printSeq = 7
		job.printStream = 7
		p.stateMu.Unlock()
		cancelled := make(chan struct{})
		var cancelOnce sync.Once
		job.printCancel = func() {
			cancelOnce.Do(func() { close(cancelled) })
		}
		starts := make(chan fsmStreamStart, 1)
		p.printBufferHook = func(_ *printJob, start int, streamID uint64) {
			starts <- fsmStreamStart{start: start, streamID: streamID}
		}

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationHold})
		waitForState(t, p, statePaused)
		requireNoFinish(t, cancelled)
		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationRetransmit, data: []byte{0x5a, 0x05, 0x00, 0x04}})
		select {
		case <-cancelled:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("printCancel was not called on retransmit")
		}
		second := requireStreamStart(t, starts)
		if second.start != 4 {
			t.Fatalf("second stream start = %d, want 4", second.start)
		}

		if ok := p.dispatchJobEvent(job, fsmEvent{kind: eventPacketsSent, streamID: 7}); ok {
			t.Fatal("stale packet completion after retransmit was accepted")
		}
		waitForState(t, p, statePrinting)
	})

	t.Run("hold while waiting for completion stays waiting", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		setFSMState(p, stateWaitingRetry)
		cancelled := make(chan struct{})
		job.printCancel = func() {
			close(cancelled)
		}

		if ok := p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationHold}); !ok {
			t.Fatal("hold in waiting retry was ignored")
		}

		waitForState(t, p, stateWaitingRetry)
		requireNoFinish(t, cancelled)
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
			job := activeTestJob(t, p)
			setFSMState(p, tc.state)
			starts := make(chan int, 1)
			p.printBufferHook = func(_ *printJob, start int, _ uint64) {
				starts <- start
			}

			p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationRetransmit, data: tc.data})

			waitForState(t, p, statePrinting)
			select {
			case got := <-starts:
				if got != tc.want {
					t.Fatalf("stream start = %d, want %d", got, tc.want)
				}
			case <-time.After(fsmWaitTimeout):
				t.Fatal("print stream was not restarted")
			}

			job.cancel()
		})
	}

	t.Run("stale packet completion from previous retransmit round is ignored", func(t *testing.T) {
		p := newFSMTestPrinter(10)
		job := activeTestJob(t, p)
		setFSMState(p, statePrinting)

		starts := make(chan fsmStreamStart, 2)
		p.printBufferHook = func(_ *printJob, start int, streamID uint64) {
			starts <- fsmStreamStart{start: start, streamID: streamID}
		}

		p.startPrintBuffer(job, 0)
		first := requireStreamStart(t, starts)
		if first.start != 0 {
			t.Fatalf("first stream start = %d, want 0", first.start)
		}

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationRetransmit, data: []byte{0x5a, 0x05, 0x00, 0x04}})
		second := requireStreamStart(t, starts)
		if second.start != 4 {
			t.Fatalf("second stream start = %d, want 4", second.start)
		}
		if second.streamID == first.streamID {
			t.Fatal("retransmit did not create a new stream ID")
		}
		if second.streamID != first.streamID+1 {
			t.Fatalf("second stream ID = %d, want %d", second.streamID, first.streamID+1)
		}

		if ok := p.dispatchJobEvent(job, fsmEvent{kind: eventPacketsSent, streamID: first.streamID}); ok {
			t.Fatal("stale packet completion was accepted")
		}
		waitForState(t, p, statePrinting)
		requireNoDone(t, job.doneCh)

		if ok := p.dispatchJobEvent(job, fsmEvent{kind: eventPacketsSent, streamID: second.streamID}); !ok {
			t.Fatal("current packet completion was ignored")
		}
		waitForState(t, p, stateWaitingRetry)
	})

	t.Run("duplicate error and cancel complete once without blocking", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		setFSMState(p, statePrinting)

		p.dispatchJobEvent(job, fsmEvent{kind: eventError})
		p.dispatchJobEvent(job, fsmEvent{kind: eventCancel})

		if err := requireDone(t, job.doneCh); !errors.Is(err, errPrintFailed) {
			t.Fatalf("done error = %v, want %v", err, errPrintFailed)
		}
		requireNoDone(t, job.doneCh)
		waitForState(t, p, stateFailed)
	})

	t.Run("cancel after failed does not block without a done receiver", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		setFSMState(p, stateFailed)
		finished := make(chan struct{})

		go func() {
			p.dispatchJobEvent(activeTestJob(t, p), fsmEvent{kind: eventCancel})
			close(finished)
		}()

		select {
		case <-finished:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("transition blocked without a done receiver")
		}
	})

	t.Run("internal event is delivered when notification queue is full", func(t *testing.T) {
		wantErr := errors.New("final failed")
		p := newFSMTestPrinter(1)
		job := activeTestJob(t, p)
		setFSMState(p, stateWaitingRetry)
		p.sendAndWaitHook = func([]byte, []byte, time.Duration) ([]byte, error) {
			return nil, wantErr
		}
		for i := 0; i < cap(job.eventCh); i++ {
			job.eventCh <- fsmEvent{kind: eventNotificationHold}
		}

		p.dispatchJobEvent(job, fsmEvent{kind: eventNotificationFinished})

		err := requireDone(t, job.doneCh)
		if !errors.Is(err, wantErr) {
			t.Fatalf("done error = %v, want %v", err, wantErr)
		}
		waitForState(t, p, stateFailed)
	})

	t.Run("stale init completion does not affect next print", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		var (
			mu         sync.Mutex
			firstJob   *printJob
			secondJob  *printJob
			initCalls  int
			streamJobs []*printJob
			secondID   uint64
		)
		firstInitStarted := make(chan struct{})
		releaseFirstInit := make(chan struct{})
		secondStreamStarted := make(chan struct{})
		var secondStreamOnce sync.Once
		p.initSequenceHook = func(job *printJob) {
			mu.Lock()
			initCalls++
			call := initCalls
			if call == 1 {
				firstJob = job
			} else if call == 2 {
				secondJob = job
			}
			mu.Unlock()

			if call == 1 {
				close(firstInitStarted)
				<-releaseFirstInit
				p.dispatchJobEvent(job, fsmEvent{kind: eventInitComplete})
				return
			}
			p.dispatchJobEvent(job, fsmEvent{kind: eventInitComplete})
		}
		p.sendAndWaitHook = func(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
			return append([]byte(nil), expectPrefix...), nil
		}
		p.printBufferHook = func(job *printJob, start int, streamID uint64) {
			mu.Lock()
			streamJobs = append(streamJobs, job)
			secondID = streamID
			mu.Unlock()
			if start != 0 {
				t.Errorf("stream start = %d, want 0", start)
			}
			secondStreamOnce.Do(func() {
				close(secondStreamStarted)
			})
		}

		ctxA, cancelA := context.WithCancel(context.Background())
		errA := make(chan error, 1)
		go func() {
			errA <- p.printPackets(ctxA, p.buffer)
		}()
		select {
		case <-firstInitStarted:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("first init did not start")
		}
		cancelA()
		if err := requireDone(t, errA); !errors.Is(err, context.Canceled) {
			t.Fatalf("first print error = %v, want context.Canceled", err)
		}

		ctxB := t.Context()
		errB := make(chan error, 1)
		go func() {
			errB <- p.printPackets(ctxB, p.buffer)
		}()
		select {
		case <-secondStreamStarted:
		case <-time.After(fsmWaitTimeout):
			t.Fatal("second print stream did not start")
		}

		close(releaseFirstInit)
		time.Sleep(fsmBlockTimeout)

		mu.Lock()
		streamCount := len(streamJobs)
		streamJob := streamJobs[0]
		gotFirstJob := firstJob
		gotSecondJob := secondJob
		gotSecondID := secondID
		mu.Unlock()
		if gotFirstJob == nil || gotSecondJob == nil {
			t.Fatal("expected both jobs to be recorded")
		}
		if streamCount != 1 {
			t.Fatalf("stream starts = %d, want 1", streamCount)
		}
		if streamJob != gotSecondJob {
			t.Fatal("stream started for a job other than the second job")
		}

		p.dispatchJobEvent(gotSecondJob, fsmEvent{kind: eventPacketsSent, streamID: gotSecondID})
		p.dispatchJobEvent(gotSecondJob, fsmEvent{kind: eventNotificationFinished})
		if err := requireDone(t, errB); err != nil {
			t.Fatalf("second print error = %v, want nil", err)
		}
	})

	t.Run("stale packet stream event does not affect next print", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		staleDone := make(chan struct{})
		var (
			mu        sync.Mutex
			firstJob  *printJob
			secondJob *printJob
			starts    int
			secondID  uint64
		)
		p.initSequenceHook = func(job *printJob) {
			p.dispatchJobEvent(job, fsmEvent{kind: eventInitComplete})
		}
		p.sendAndWaitHook = func(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
			return append([]byte(nil), expectPrefix...), nil
		}
		p.printBufferHook = func(job *printJob, start int, streamID uint64) {
			mu.Lock()
			starts++
			call := starts
			if call == 1 {
				firstJob = job
			} else if call == 2 {
				secondJob = job
				secondID = streamID
			}
			mu.Unlock()
			if call == 1 {
				go func() {
					<-staleDone
					p.dispatchJobEvent(job, fsmEvent{kind: eventPacketsSent, streamID: streamID})
				}()
			}
		}

		ctxA, cancelA := context.WithCancel(context.Background())
		errA := make(chan error, 1)
		go func() {
			errA <- p.printPackets(ctxA, p.buffer)
		}()
		waitUntil(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return firstJob != nil
		})
		cancelA()
		if err := requireDone(t, errA); !errors.Is(err, context.Canceled) {
			t.Fatalf("first print error = %v, want context.Canceled", err)
		}

		ctxB := t.Context()
		errB := make(chan error, 1)
		go func() {
			errB <- p.printPackets(ctxB, p.buffer)
		}()
		waitUntil(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return secondJob != nil
		})

		close(staleDone)
		time.Sleep(fsmBlockTimeout)
		waitForState(t, p, statePrinting)
		requireNoDone(t, errB)

		mu.Lock()
		gotSecondJob := secondJob
		gotSecondID := secondID
		mu.Unlock()
		p.dispatchJobEvent(gotSecondJob, fsmEvent{kind: eventPacketsSent, streamID: gotSecondID})
		p.dispatchJobEvent(gotSecondJob, fsmEvent{kind: eventNotificationFinished})
		if err := requireDone(t, errB); err != nil {
			t.Fatalf("second print error = %v, want nil", err)
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
			job := activeTestJob(t, p)
			notifyCh := make(chan lxd02notification, 1)
			ctx := t.Context()
			go p.worker(ctx, notifyCh)

			notifyCh <- tc.ntf
			got := requireEvent(t, job.eventCh, tc.want)
			if tc.want == eventNotificationRetransmit && !bytes.Equal(got.data, tc.ntf.data) {
				t.Fatalf("retransmit data = % x, want % x", got.data, tc.ntf.data)
			}
		})
	}

	t.Run("no active event receiver does not block worker", func(t *testing.T) {
		p := newFSMTestPrinter(1)
		p.stateMu.Lock()
		p.activeJob = nil
		p.stateMu.Unlock()
		notifyCh := make(chan lxd02notification)
		ctx := t.Context()
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

func waitUntil(t *testing.T, fn func() bool) {
	t.Helper()

	deadline := time.After(fsmWaitTimeout)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if fn() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for condition")
		case <-tick.C:
		}
	}
}
