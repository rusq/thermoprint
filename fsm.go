package thermoprint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/looplab/fsm"
)

type printerState int

//go:generate go tool stringer -type=printerState -trimprefix=state
const (
	stateIdle printerState = iota
	stateInitializing
	statePrinting
	statePaused
	stateWaitingRetry
	stateCompleted
	stateFailed
)

type printerEvent int

//go:generate go tool stringer -type=printerEvent -trimprefix=event
const (
	eventStart printerEvent = iota
	eventNotificationHold
	eventNotificationRetransmit
	eventNotificationFinished
	eventInitComplete
	eventPacketsSent
	eventCancel
	eventError
)

var errPrintFailed = errors.New("print job failed")

type fsmEvent struct {
	kind     printerEvent
	data     []byte
	err      error
	streamID uint64
}

type printJob struct {
	fsm         *fsm.FSM
	eventCh     chan fsmEvent
	doneCh      chan error
	doneOnce    sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
	printCancel context.CancelFunc
	printStream uint64
	printSeq    uint64
}

func (p *LXD02) newPrintJob(ctx context.Context) *printJob {
	jobCtx, cancel := context.WithCancel(ctx)
	job := &printJob{
		eventCh: make(chan fsmEvent, 10),
		doneCh:  make(chan error, 1),
		ctx:     jobCtx,
		cancel:  cancel,
	}
	job.fsm = p.newPrintFSM(job, stateIdle)
	return job
}

func (p *LXD02) newPrintFSM(job *printJob, initial printerState) *fsm.FSM {
	states := []string{
		stateIdle.String(),
		stateInitializing.String(),
		statePrinting.String(),
		statePaused.String(),
		stateWaitingRetry.String(),
		stateCompleted.String(),
		stateFailed.String(),
	}

	return fsm.NewFSM(
		initial.String(),
		fsm.Events{
			{Name: eventStart.String(), Src: []string{stateIdle.String(), stateCompleted.String(), stateFailed.String()}, Dst: stateInitializing.String()},
			{Name: eventInitComplete.String(), Src: []string{stateInitializing.String()}, Dst: statePrinting.String()},
			{Name: eventPacketsSent.String(), Src: []string{statePrinting.String()}, Dst: stateWaitingRetry.String()},
			{Name: eventNotificationHold.String(), Src: []string{statePrinting.String()}, Dst: statePaused.String()},
			{Name: eventNotificationRetransmit.String(), Src: []string{statePrinting.String(), statePaused.String(), stateWaitingRetry.String()}, Dst: statePrinting.String()},
			{Name: eventNotificationFinished.String(), Src: []string{stateWaitingRetry.String()}, Dst: stateCompleted.String()},
			{Name: eventCancel.String(), Src: states, Dst: stateFailed.String()},
			{Name: eventError.String(), Src: states, Dst: stateFailed.String()},
		},
		fsm.Callbacks{
			"enter_state": func(_ context.Context, e *fsm.Event) {
				p.setStateForJob(job, fsmStateToPrinterState(e.Dst))
			},
			"after_" + eventStart.String(): func(_ context.Context, _ *fsm.Event) {
				slog.Info("Starting printer initialization")
				go p.startInitSequence(job)
			},
			"after_" + eventInitComplete.String(): func(_ context.Context, _ *fsm.Event) {
				go p.beginPrint(job)
			},
			"after_" + eventPacketsSent.String(): func(_ context.Context, _ *fsm.Event) {
				slog.Info("All packets sent, waiting for printer to complete (5a06)")
			},
			"after_" + eventNotificationHold.String(): func(_ context.Context, _ *fsm.Event) {
				slog.Warn("Hold signal received, pausing print job")
				p.cancelPrintBuffer(job)
			},
			"after_" + eventNotificationRetransmit.String(): func(_ context.Context, e *fsm.Event) {
				packet := extractRetryPacketIndex(eventData(e))
				slog.Warn("Retransmit request", "packet", packet)
				p.cancelPrintBuffer(job)
				go p.startPrintBuffer(job, packet)
			},
			"after_" + eventNotificationFinished.String(): func(_ context.Context, _ *fsm.Event) {
				p.finishPrint(job)
			},
			"after_" + eventCancel.String(): func(_ context.Context, e *fsm.Event) {
				p.failPrint(job, eventErr(e, context.Canceled))
			},
			"after_" + eventError.String(): func(_ context.Context, e *fsm.Event) {
				p.failPrint(job, eventErr(e, errPrintFailed))
			},
		},
	)
}

func (p *LXD02) runFSM(job *printJob) {
	for {
		select {
		case <-job.ctx.Done():
			slog.Debug("FSM context done, exiting")
			return
		case evt, ok := <-job.eventCh:
			if !ok {
				slog.Debug("FSM event channel closed, exiting")
				return
			}
			p.dispatchJobEvent(job, evt)
		}
	}
}

func (p *LXD02) dispatchJobEvent(job *printJob, evt fsmEvent) bool {
	if !p.isActiveJob(job) {
		slog.Warn("Ignoring stale FSM event", "event", evt.kind)
		return false
	}
	if !p.isCurrentPrintStream(job, evt) {
		slog.Warn("Ignoring stale packet stream event", "event", evt.kind, "stream", evt.streamID)
		return false
	}

	log := slog.With("state", job.fsm.Current(), "event", evt.kind)
	if err := job.fsm.Event(context.Background(), evt.kind.String(), evt.data, evt.err); err != nil {
		var invalid fsm.InvalidEventError
		var unknown fsm.UnknownEventError
		switch {
		case errors.As(err, &invalid):
			log.Warn("Ignoring inappropriate FSM event", "error", err)
		case errors.As(err, &unknown):
			log.Warn("Ignoring unknown FSM event", "error", err)
		default:
			log.Warn("FSM event returned error", "error", err)
		}
	}
	p.setStateForJob(job, fsmStateToPrinterState(job.fsm.Current()))
	return true
}

func (p *LXD02) beginPrint(job *printJob) {
	if !p.isActiveJob(job) {
		return
	}
	buflen := len(p.buffer)
	if buflen == 0 {
		slog.Error("Buffer is empty, cannot start printing")
		p.dispatchJobEvent(job, fsmEvent{kind: eventError, err: errBufferEmpty})
		return
	}

	m := byte((buflen >> 8) & 0xFF)
	n := byte(buflen & 0xFF)
	beginCmd := []byte{0x5a, 0x04, m, n, 0x00, 0x00}
	resp, err := p.sendAndWaitForFSM(beginCmd, beginCmd[:2], 3*time.Second)
	if err != nil {
		slog.Error("Failed to send initial print command", "error", err)
		p.dispatchJobEvent(job, fsmEvent{kind: eventError, err: fmt.Errorf("send initial print command: %w", err)})
		return
	}
	if !p.isActiveJob(job) {
		return
	}
	slog.Debug("Initial print command ack", "response", fmt.Sprintf("% x", resp))
	p.startPrintBuffer(job, 0)
}

func (p *LXD02) finishPrint(job *printJob) {
	if !p.isActiveJob(job) {
		return
	}
	buflen := len(p.buffer)
	m := byte((buflen >> 8) & 0xFF)
	n := byte(buflen & 0xFF)
	finalCmd := []byte{0x5a, 0x04, m, n, 0x01, 0x00}
	resp, err := p.sendAndWaitForFSM(finalCmd, finalCmd[:2], 3*time.Second)
	if err != nil {
		slog.Error("Failed to send final end command", "error", err)
		p.dispatchJobEvent(job, fsmEvent{kind: eventError, err: fmt.Errorf("send final end command: %w", err)})
		return
	}
	if !p.isActiveJob(job) {
		return
	}
	slog.Debug("Final end-of-transmission command ack", "response", fmt.Sprintf("% x", resp))
	p.completePrint(job, nil)
}

func (p *LXD02) failPrint(job *printJob, err error) {
	p.cancelPrintBuffer(job)
	p.completePrint(job, err)
}

func (p *LXD02) completePrint(job *printJob, err error) {
	job.doneOnce.Do(func() {
		job.cancel()
		p.cancelPrintBuffer(job)
		select {
		case job.doneCh <- err:
		default:
		}
	})
}

func (p *LXD02) cancelPrintBuffer(job *printJob) {
	p.stateMu.Lock()
	cancel := job.printCancel
	job.printCancel = nil
	if p.activeJob == job {
		job.printStream = 0
	}
	p.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *LXD02) currentJob() *printJob {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.activeJob
}

func (p *LXD02) isActiveJob(job *printJob) bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return job != nil && p.activeJob == job
}

func (p *LXD02) isCurrentPrintStream(job *printJob, evt fsmEvent) bool {
	if evt.streamID == 0 {
		return evt.kind != eventPacketsSent
	}

	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.activeJob == job && job.printStream == evt.streamID
}

func (p *LXD02) nextPrintStream(job *printJob) (uint64, bool) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.activeJob != job {
		return 0, false
	}
	job.printSeq++
	job.printStream = job.printSeq
	return job.printStream, true
}

func (p *LXD02) setStateForJob(job *printJob, state printerState) {
	p.stateMu.Lock()
	if p.activeJob == job {
		p.state = state
	}
	p.stateMu.Unlock()
}

func (p *LXD02) routeNotificationEvent(evt fsmEvent) bool {
	job := p.currentJob()
	if job == nil {
		slog.Warn("Ignoring printer notification with no active print", "event", evt.kind)
		return false
	}
	select {
	case job.eventCh <- evt:
		return true
	default:
		slog.Warn("Dropping printer notification because event channel is not ready", "event", evt.kind)
		return false
	}
}

func (p *LXD02) startInitSequence(job *printJob) {
	if p.initSequenceHook != nil {
		p.initSequenceHook(job)
		return
	}
	p.sendInitSequence(job)
}

func (p *LXD02) sendAndWaitForFSM(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
	if p.sendAndWaitHook != nil {
		return p.sendAndWaitHook(data, expectPrefix, timeout)
	}
	return p.sendAndWait(data, expectPrefix, timeout)
}

func (p *LXD02) startPrintBuffer(job *printJob, start int) {
	streamID, ok := p.nextPrintStream(job)
	if !ok {
		return
	}
	if p.printBufferHook != nil {
		p.printBufferHook(job, start, streamID)
		return
	}
	p.printBuffer(job, start, streamID)
}

func fsmStateToPrinterState(state string) printerState {
	switch state {
	case stateIdle.String():
		return stateIdle
	case stateInitializing.String():
		return stateInitializing
	case statePrinting.String():
		return statePrinting
	case statePaused.String():
		return statePaused
	case stateWaitingRetry.String():
		return stateWaitingRetry
	case stateCompleted.String():
		return stateCompleted
	case stateFailed.String():
		return stateFailed
	default:
		return stateFailed
	}
}

func eventData(e *fsm.Event) []byte {
	if len(e.Args) == 0 {
		return nil
	}
	data, _ := e.Args[0].([]byte)
	return data
}

func eventErr(e *fsm.Event, fallback error) error {
	if len(e.Args) > 1 {
		if err, ok := e.Args[1].(error); ok && err != nil {
			return err
		}
	}
	return fallback
}
