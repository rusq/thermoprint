package printers

import (
	"context"
	"log/slog"
)

type printerState int

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

const (
	eventStart printerEvent = iota
	eventNotificationHold
	eventNotificationRetransmit
	eventNotificationFinished
	eventNotificationStatus
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
		if evt == eventNotificationStatus {
			log.Info("Printer ready after status")
			p.state = stateReady
			go func() {
				p.stateMu.Lock()
				p.state = statePrinting
				p.stateMu.Unlock()
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

		case eventNotificationFinished:
			log.Info("Printer reports print complete, sending finalization")
			p.state = stateCompleted

			go func() {
				buflen := len(p.buffer)
				m := byte((buflen >> 8) & 0xFF)
				n := byte(buflen & 0xFF)
				finalCmd := []byte{0x5a, 0x04, m, n, 0x01, 0x00}
				if err := p.send(finalCmd); err != nil {
					slog.Error("Failed to send final end command", "error", err)
					p.eventCh <- fsmEvent{kind: eventError}
					return
				}
				slog.Info("Final end-of-transmission command sent")
				p.doneCh <- struct{}{}
			}()

		case eventError:
			log.Error("Error occurred during print")
			if p.printCancel != nil {
				p.printCancel()
			}
			p.state = stateFailed
			p.doneCh <- struct{}{}
		}

	case statePaused:
		if evt == eventNotificationRetransmit {
			packet := extractRetryPacketIndex(data)
			log.Info("Resuming print after hold", "packet", packet)
			p.state = statePrinting
			go p.printBuffer(packet)
		}

	case stateCompleted:
		log.Info("Ignoring event, print job already completed")

	case stateFailed:
		log.Warn("Already in failed state, ignoring event")

	default:
		log.Warn("Unhandled state", "state", p.state, "event", evt)
	}

	// Global cancellation or error
	if evt == eventCancel || evt == eventError {
		if p.printCancel != nil {
			p.printCancel()
		}
		log.Error("Job canceled or failed")
		p.state = stateFailed
		p.doneCh <- struct{}{}
	}
}
