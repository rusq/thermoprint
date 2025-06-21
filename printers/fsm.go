package printers

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type printerState int

//go:generate stringer -type=printerState -trimprefix=state
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

//go:generate stringer -type=printerEvent -trimprefix=event
const (
	eventStart printerEvent = iota
	eventNotificationHold
	eventNotificationRetransmit
	eventNotificationFinished
	eventInitComplete
	eventCancel
	eventError
)

type fsmEvent struct {
	kind printerEvent
	data []byte
}

func (p *LXD02) runFSM(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			p.transition(eventCancel, nil)
			return
		case evt := <-p.eventCh:
			p.transition(evt.kind, evt.data)
		}
	}
}

func (p *LXD02) transition(evt printerEvent, data []byte) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	log := slog.With("state", p.state, "event", evt)

	switch p.state {

	case stateIdle:
		if evt == eventStart {
			log.Info("Starting printer initialization")
			p.state = stateInitializing
			go p.sendInitSequence()
		}

	case stateInitializing:
		if evt == eventInitComplete {
			log.Info("Printer ready after status")
			p.state = stateReady
			go func() {
				slog.Debug("switching to printing state")
				p.stateMu.Lock()
				p.state = statePrinting
				p.stateMu.Unlock()
				buflen := len(p.buffer)
				if buflen == 0 {
					slog.Error("Buffer is empty, cannot start printing")
					p.eventCh <- fsmEvent{kind: eventError}
					return
				}
				m := byte((buflen >> 8) & 0xFF)
				n := byte(buflen & 0xFF)

				slog.Debug("Sending initial print command")
				beginCmd := []byte{0x5a, 0x04, m, n, 0x00, 0x00}
				resp, err := p.sendAndWait(beginCmd, beginCmd[:2], 3*time.Second)
				if err != nil {
					slog.Error("Failed to send initial print command", "error", err)
					p.eventCh <- fsmEvent{kind: eventError}
					return
				}
				slog.Debug("Initial print command ack", "response", fmt.Sprintf("% x", resp))
				p.printBuffer(0)
			}()
		}

	case statePrinting:
		switch evt {

		case eventNotificationHold:
			log.Warn("Hold signal received, pausing print job")
			if p.printCancel != nil {
				p.printCancel()
			}
			p.state = statePaused

		case eventNotificationRetransmit:
			packet := extractRetryPacketIndex(data)
			log.Warn("Retransmit request", "packet", packet)
			if p.printCancel != nil {
				p.printCancel()
			}
			p.state = statePrinting
			go p.printBuffer(packet)
		case eventError:
			log.Error("Error occurred during print")
			if p.printCancel != nil {
				p.printCancel()
			}
			p.state = stateFailed
			p.doneCh <- struct{}{}
		case eventNotificationFinished:
			log.Info("data sent, waiting for printer to complete")
			p.state = stateWaitingRetry
		default:
			log.Warn("Unexpected event during printing", "event", evt)
		}

	case stateWaitingRetry:
		switch evt {
		case eventNotificationFinished:
			log.Info("Printer reports print complete, sending finalization")
			p.state = stateCompleted

			buflen := len(p.buffer)
			m := byte((buflen >> 8) & 0xFF)
			n := byte(buflen & 0xFF)
			finalCmd := []byte{0x5a, 0x04, m, n, 0x01, 0x00}
			resp, err := p.sendAndWait(finalCmd, finalCmd[:2], 3*time.Second)
			if err != nil {
				slog.Error("Failed to send final end command", "error", err)
				p.eventCh <- fsmEvent{kind: eventError}
				return
			}
			slog.Debug("Final end-of-transmission command ack", "response", fmt.Sprintf("% x", resp))
			p.doneCh <- struct{}{}
		case eventNotificationHold:
			// holding
		case eventNotificationRetransmit:
			packet := extractRetryPacketIndex(data)
			log.Warn("Retransmit request in waiting retry state", "packet", packet)
			p.state = statePrinting
			go p.printBuffer(packet)
		default:
			log.Warn("Unexpected event in waiting retry state", "event", evt)
		}

	case statePaused:
		if evt == eventNotificationRetransmit {
			packet := extractRetryPacketIndex(data)
			log.Info("Resuming print after hold", "packet", packet)
			p.state = statePrinting
			go p.printBuffer(packet)
		}

	case stateCompleted:
		if evt == eventCancel {
			// nothing
		} else {
			log.Info("Ignoring event, print job already completed")
		}

	case stateFailed:
		log.Warn("Already in failed state, ignoring event")

	default:
		log.Warn("Unhandled state", "state", p.state, "event", evt)
	}

	// Global cancellation or error
	if p.state != stateCompleted && (evt == eventCancel || evt == eventError) {
		if p.printCancel != nil {
			p.printCancel()
		}
		log.Error("Job canceled or failed")
		p.state = stateFailed
		p.doneCh <- struct{}{}
	}
}
