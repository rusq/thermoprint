package thermoprint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/looplab/fsm"
)

type printerState int

//go:generate go tool stringer -type=printerState -trimprefix=state
const (
	stateIdle printerState = iota
	stateInitializing
	stateReady
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
	kind printerEvent
	data []byte
	err  error
}

func (p *LXD02) newPrintFSM(initial printerState) *fsm.FSM {
	states := []string{
		stateIdle.String(),
		stateInitializing.String(),
		stateReady.String(),
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
				p.setState(fsmStateToPrinterState(e.Dst))
			},
			"after_" + eventStart.String(): func(_ context.Context, _ *fsm.Event) {
				slog.Info("Starting printer initialization")
				go p.startInitSequence()
			},
			"after_" + eventInitComplete.String(): func(_ context.Context, _ *fsm.Event) {
				go p.beginPrint()
			},
			"after_" + eventPacketsSent.String(): func(_ context.Context, _ *fsm.Event) {
				slog.Info("All packets sent, waiting for printer to complete (5a06)")
			},
			"after_" + eventNotificationHold.String(): func(_ context.Context, _ *fsm.Event) {
				slog.Warn("Hold signal received, pausing print job")
				p.cancelPrintBuffer()
			},
			"after_" + eventNotificationRetransmit.String(): func(_ context.Context, e *fsm.Event) {
				packet := extractRetryPacketIndex(eventData(e))
				slog.Warn("Retransmit request", "packet", packet)
				p.cancelPrintBuffer()
				go p.startPrintBuffer(packet)
			},
			"after_" + eventNotificationFinished.String(): func(_ context.Context, _ *fsm.Event) {
				p.finishPrint()
			},
			"after_" + eventCancel.String(): func(_ context.Context, e *fsm.Event) {
				p.failPrint(eventErr(e, context.Canceled))
			},
			"after_" + eventError.String(): func(_ context.Context, e *fsm.Event) {
				p.failPrint(eventErr(e, errPrintFailed))
			},
		},
	)
}

func (p *LXD02) runFSM(ctx context.Context) {
	eventCh := p.currentEventCh()
	if eventCh == nil {
		slog.Warn("FSM requested without an event channel")
		return
	}

	for {
		select {
		case <-ctx.Done():
			slog.Debug("FSM context done, exiting")
			return
		case evt, ok := <-eventCh:
			if !ok {
				slog.Debug("FSM event channel closed, exiting")
				return
			}
			p.transitionEvent(evt)
		}
	}
}

func (p *LXD02) transition(evt printerEvent, data []byte) {
	p.transitionEvent(fsmEvent{kind: evt, data: data})
}

func (p *LXD02) transitionEvent(evt fsmEvent) {
	machine := p.currentFSM()
	if machine == nil {
		slog.Warn("Ignoring FSM event with no active print", "event", evt.kind)
		if evt.kind == eventCancel || evt.kind == eventError {
			p.setState(stateFailed)
			p.failPrint(eventErrFromFSMEvent(evt, errPrintFailed))
		}
		return
	}

	log := slog.With("state", machine.Current(), "event", evt.kind)
	if err := machine.Event(context.Background(), evt.kind.String(), evt.data, evt.err); err != nil {
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
	p.setState(fsmStateToPrinterState(machine.Current()))
}

func (p *LXD02) beginPrint() {
	buflen := len(p.buffer)
	if buflen == 0 {
		slog.Error("Buffer is empty, cannot start printing")
		p.emitEvent(fsmEvent{kind: eventError, err: errBufferEmpty})
		return
	}

	m := byte((buflen >> 8) & 0xFF)
	n := byte(buflen & 0xFF)
	beginCmd := []byte{0x5a, 0x04, m, n, 0x00, 0x00}
	resp, err := p.sendAndWaitForFSM(beginCmd, beginCmd[:2], 3*time.Second)
	if err != nil {
		slog.Error("Failed to send initial print command", "error", err)
		p.emitEvent(fsmEvent{kind: eventError, err: fmt.Errorf("send initial print command: %w", err)})
		return
	}
	slog.Debug("Initial print command ack", "response", fmt.Sprintf("% x", resp))
	p.startPrintBuffer(0)
}

func (p *LXD02) finishPrint() {
	buflen := len(p.buffer)
	m := byte((buflen >> 8) & 0xFF)
	n := byte(buflen & 0xFF)
	finalCmd := []byte{0x5a, 0x04, m, n, 0x01, 0x00}
	resp, err := p.sendAndWaitForFSM(finalCmd, finalCmd[:2], 3*time.Second)
	if err != nil {
		slog.Error("Failed to send final end command", "error", err)
		p.emitEvent(fsmEvent{kind: eventError, err: fmt.Errorf("send final end command: %w", err)})
		return
	}
	slog.Debug("Final end-of-transmission command ack", "response", fmt.Sprintf("% x", resp))
	p.completePrint(nil)
}

func (p *LXD02) failPrint(err error) {
	p.cancelPrintBuffer()
	p.completePrint(err)
}

func (p *LXD02) completePrint(err error) {
	p.doneOnce.Do(func() {
		doneCh := p.currentDoneCh()
		if doneCh == nil {
			return
		}
		select {
		case doneCh <- err:
		default:
		}
	})
}

func (p *LXD02) cancelPrintBuffer() {
	p.stateMu.Lock()
	cancel := p.printCancel
	p.printCancel = nil
	p.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *LXD02) currentFSM() *fsm.FSM {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.printFSM
}

func (p *LXD02) currentEventCh() chan fsmEvent {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.eventCh
}

func (p *LXD02) currentDoneCh() chan error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.doneCh
}

func (p *LXD02) setState(state printerState) {
	p.stateMu.Lock()
	p.state = state
	p.stateMu.Unlock()
}

func (p *LXD02) emitEvent(evt fsmEvent) bool {
	eventCh := p.currentEventCh()
	if eventCh == nil {
		slog.Warn("Ignoring FSM event with no active print", "event", evt.kind)
		return false
	}
	select {
	case eventCh <- evt:
		return true
	default:
		slog.Warn("Dropping FSM event because event channel is full", "event", evt.kind)
		return false
	}
}

func (p *LXD02) routeNotificationEvent(evt fsmEvent) bool {
	eventCh := p.currentEventCh()
	if eventCh == nil {
		slog.Warn("Ignoring printer notification with no active print", "event", evt.kind)
		return false
	}
	select {
	case eventCh <- evt:
		return true
	default:
		slog.Warn("Dropping printer notification because event channel is not ready", "event", evt.kind)
		return false
	}
}

func (p *LXD02) startInitSequence() {
	if p.initSequenceHook != nil {
		p.initSequenceHook()
		return
	}
	p.sendInitSequence()
}

func (p *LXD02) sendAndWaitForFSM(data []byte, expectPrefix []byte, timeout time.Duration) ([]byte, error) {
	if p.sendAndWaitHook != nil {
		return p.sendAndWaitHook(data, expectPrefix, timeout)
	}
	return p.sendAndWait(data, expectPrefix, timeout)
}

func (p *LXD02) startPrintBuffer(start int) {
	if p.printBufferHook != nil {
		p.printBufferHook(start)
		return
	}
	p.printBuffer(start)
}

func fsmStateToPrinterState(state string) printerState {
	switch state {
	case stateIdle.String():
		return stateIdle
	case stateInitializing.String():
		return stateInitializing
	case stateReady.String():
		return stateReady
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

func eventErrFromFSMEvent(evt fsmEvent, fallback error) error {
	if evt.err != nil {
		return evt.err
	}
	return fallback
}
