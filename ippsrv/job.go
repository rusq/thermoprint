package ippsrv

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"sync"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/looplab/fsm"
)

type Job struct {
	mu sync.RWMutex

	ID           JobID
	Printer      Printer // Printer that this job is associated with
	State        JobState
	StateReasons []JobStateReason // Reasons for the current job state
	Name         string
	Created      time.Time
	Processing   time.Time
	Completed    time.Time
	Username     string // Username of the user who created the job
	JobURI       string // URL to access the job, e.g., "/printers/default/123"
	PrinterURI   string // URI of the printer, e.g., "/printers/default"
	Format       string // document-format of the job data, if provided by the client

	sm           *fsm.FSM
	buffer       []byte // Buffer for job data, if needed
	printOptions printJobOptions
}

type JobID int32

// JobState represents the state of a job.
// https://datatracker.ietf.org/doc/html/rfc2911#section-4.3.7
//
//go:generate go tool stringer -trimprefix Job -type JobState
type JobState int32

const (
	JobPending JobState = iota + 3
	JobPendingHeld
	JobProcessing
	JobProcessingStopped
	JobCancelled
	JobAborted
	JobCompleted
)

// fsm events for job state transitions.
const (
	jobEvtHeld     = "held"
	jobEvtResume   = "resume"
	jobEvtProcess  = "process"
	jobEvtStop     = "stop"
	jobEvtAbort    = "abort"
	jobEvtComplete = "complete"
	jobEvtCancel   = "cancel"
)

/*
https://datatracker.ietf.org/doc/html/rfc8011#page-128

                                                      +----> canceled
                                                     /
       +----> pending  -------> processing ---------+------> completed
       |         ^                   ^               \
   --->+         |                   |                +----> aborted
       |         v                   v               /
       +----> pending-held    processing-stopped ---+
*/

var jobFsmEvts = []fsm.EventDesc{
	{
		Name: jobEvtHeld,
		Src:  []string{JobPending.String()},
		Dst:  JobPendingHeld.String(),
	},
	{
		Name: jobEvtResume,
		Src:  []string{JobPendingHeld.String()},
		Dst:  JobPending.String(),
	},
	{
		Name: jobEvtProcess, // event args: []byte{data to print}
		Src:  []string{JobPending.String()},
		Dst:  JobProcessing.String(),
	},
	{
		Name: jobEvtStop,
		Src:  []string{JobProcessing.String()},
		Dst:  JobProcessingStopped.String(),
	},
	{
		Name: jobEvtResume,
		Src:  []string{JobProcessingStopped.String()},
		Dst:  JobProcessing.String(),
	},
	{
		Name: jobEvtCancel, // event args: JobStateReason...
		Src:  []string{JobProcessing.String()},
		Dst:  JobCancelled.String(),
	},
	{
		Name: jobEvtComplete,
		Src:  []string{JobProcessing.String()},
		Dst:  JobCompleted.String(),
	},
	{
		Name: jobEvtAbort, // event args: JobStateReason...
		Src: []string{
			JobProcessing.String(),
			JobProcessingStopped.String(),
		},
		Dst: JobAborted.String(),
	},
}

// JobStateReason represents the reason for the current job state.
// https://datatracker.ietf.org/doc/html/rfc2911#section-4.3.8
// RFC2911 obsoleted by RFC8011.
type JobStateReason string

const (
	JSRNone                      JobStateReason = "none"
	JSRJobIncoming               JobStateReason = "job-incoming"
	JSRJobDataInsufficient       JobStateReason = "job-data-insufficient"
	JSRDocumentAccessError       JobStateReason = "document-access-error"
	JSRSubmissionInterrupted     JobStateReason = "submission-interrupted"
	JSRJobOutgoing               JobStateReason = "job-outgoing"
	JSRJobHeldUntilSpecified     JobStateReason = "job-held-until-specified"
	JSRResourcesAreNotReady      JobStateReason = "resources-are-not-ready"
	JSRJobQueued                 JobStateReason = "job-queued"
	JSRJobTransforming           JobStateReason = "job-transforming"
	JSRJobPrinting               JobStateReason = "job-printing"
	JSRJobCancelledByUser        JobStateReason = "job-cancelled-by-user"
	JSRJobCancelledByOperator    JobStateReason = "job-cancelled-by-operator"
	JSRJobCancelledAtDevice      JobStateReason = "job-cancelled-at-device"
	JSRAbortedBySystem           JobStateReason = "aborted-by-system"
	JSRUnsupportedCompression    JobStateReason = "unsupported-compression"
	JSRUnsupportedDocumentFormat JobStateReason = "unsupported-document-format"
	JSRDocumentFormatError       JobStateReason = "document-format-error"
	JSRProcessingToStopPoint     JobStateReason = "processing-to-stop-point"
	JSRServiceOffline            JobStateReason = "service-offline"
	JSRJobCompletedSuccessfully  JobStateReason = "job-completed-successfully"
	JSRJobCompletedWithWarnings  JobStateReason = "job-completed-with-warnings"
	JSRJobCompletedWithErrors    JobStateReason = "job-completed-with-errors"
	JSRJobRestartable            JobStateReason = "job-restartable"
	JSRQueuedInDevice            JobStateReason = "queued-in-device"
	JSROther                     JobStateReason = "other"
)

// createJobFromRequest creates a new Job from the given IPP request.
func createJobFromRequest(p Printer, baseURL string, id JobID, req *goipp.Message) (*Job, error) {
	// Extract job name and username from the request
	jobName, err := extractValue[goipp.String](req.Operation, "job-name")
	if err != nil {
		slog.Warn("failed to extract job-name", "error", err)
		jobName = goipp.String(fmt.Sprintf("Job-%d", id)) // Default to "Job-ID" if not provided
	}
	username, err := extractValue[goipp.String](req.Operation, "requesting-user-name")
	if err != nil {
		slog.Warn("failed to extract requesting-user-name", "error", err)
		username = goipp.String("unknown") // Default to "unknown" if not provided
	}
	printerURI, err := extractValue[goipp.String](req.Operation, "printer-uri")
	if err != nil {
		return nil, fmt.Errorf("failed to extract printer-uri: %w", err)
	}
	// document-format is optional; used for logging only, the data format is
	// sniffed at print time.
	format, err := extractValue[goipp.String](req.Operation, "document-format")
	if err != nil {
		format = ""
	} else {
		slog.Debug("job document format", "job_id", id, "document_format", format)
	}

	jobURL := path.Join(baseURL, p.Name(), fmt.Sprintf("%d", id))

	job, err := createJob(p, id, printerURI.String(), jobURL, jobName.String(), username.String(), format.String())
	if err != nil {
		return nil, err
	}
	job.printOptions.trimTrailingBlank = requestAllowsTrailingBlankTrim(req, p)
	return job, nil
}

func createJob(p Printer, id JobID, printerURI, jobURL, name, username, format string) (*Job, error) {
	// Create a new job based on the message
	job := &Job{
		ID:           id,
		State:        JobPending,
		StateReasons: []JobStateReason{JSRJobIncoming, JSRJobDataInsufficient},
		Printer:      p,
		Name:         name,
		Created:      time.Now(),
		Processing:   time.Time{},
		Completed:    time.Time{},
		Username:     username,
		JobURI:       jobURL,
		PrinterURI:   printerURI,
		Format:       format,
	}
	job.sm = makeJobFSM(job)

	return job, nil
}

func makeJobFSM(j *Job) *fsm.FSM {
	lg := slog.With("job_id", j.ID, "job_name", j.Name, "printer", j.Printer.Name())
	// Create a new FSM for the job with the initial state
	return fsm.NewFSM(
		JobPending.String(),
		jobFsmEvts,
		fsm.Callbacks{
			jobEvtHeld: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job held")
				j.setState(JobPendingHeld, e.Args, JSRJobHeldUntilSpecified)
			},
			jobEvtResume: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job resumed")
				j.setState(JobPending, nil)
			},
			jobEvtProcess: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job processing started")

				j.setState(JobProcessing, nil, JSRJobPrinting, JSRJobTransforming)

				// args should contain the data to print
				if len(e.Args) == 0 {
					lg.WarnContext(ctx, "No data provided for job processing")
					// send the abort event if no data is provided, as we cannot recover.
					if err := e.FSM.Event(ctx, jobEvtAbort, JSRJobDataInsufficient, JSRAbortedBySystem); err != nil {
						lg.ErrorContext(ctx, "Failed to send abort event for job processing", "error", err)
					}
					return
				} else if len(e.Args) > 1 {
					lg.WarnContext(ctx, "Too many arguments provided for job processing, using only first arg", "args_count", len(e.Args))
				}

				var data []byte
				if b, ok := e.Args[0].([]byte); !ok {
					lg.WarnContext(ctx, "Invalid argument type for job processing, expected []byte", "arg_type", fmt.Sprintf("%T", e.Args[0]))
					// send the abort event if the argument is not of type []byte
					if err := e.FSM.Event(ctx, jobEvtAbort, JSRJobDataInsufficient, JSRAbortedBySystem); err != nil {
						lg.ErrorContext(ctx, "Failed to send abort event for job processing", "error", err)
					}
					return
				} else {
					data = b
					j.mu.Lock()
					j.buffer = data // Store the data in the job buffer, for potential reprocessing
					j.mu.Unlock()

				}

				// Concurrent jobs for the same printer are serialised by the
				// spool (see spool.lockPrinter).
				j.Printer.SetState(PSProcessing) // Set the printer state to processing
				j.mu.Lock()
				j.Processing = time.Now() // Set the processing time to now
				j.mu.Unlock()
				// Call the printer's Print method with the job data
				if err := printWithOptions(ctx, j.Printer, data, j.printOptions); err != nil {
					lg.ErrorContext(ctx, "Failed to print job data", "error", err)
					// If printing fails, we can abort the job
					if err := e.FSM.Event(ctx, jobEvtAbort, JSRDocumentFormatError, JSRAbortedBySystem); err != nil {
						lg.ErrorContext(ctx, "Failed to send abort event for job processing", "error", err)
					}
					j.Printer.SetState(PSIdle) // Reset the printer state to idle
					// TODO: job reprocess, if the printer is in stopped state.
					return
				}
				j.Printer.SetState(PSIdle) // Reset the printer state to idle after processing
				j.mu.Lock()
				j.buffer = nil // Clear the job buffer after processing
				j.mu.Unlock()

				// Trigger job completion event
				if err := e.FSM.Event(ctx, jobEvtComplete); err != nil {
					lg.ErrorContext(ctx, "Failed to send job completion event", "error", err)
				}
			},
			jobEvtStop: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job processing stopped")
				j.setState(JobProcessingStopped, e.Args, JSRProcessingToStopPoint)
			},
			jobEvtAbort: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job aborted")
				j.setState(JobAborted, e.Args, JSRAbortedBySystem)
			},
			jobEvtComplete: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job completed")
				j.setState(JobCompleted, nil, JSRJobCompletedSuccessfully)
			},
			jobEvtCancel: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job cancelled")
				j.setState(JobCancelled, e.Args, JSRJobCancelledByUser)
			},
		},
	)
}

// setState transitions the job into state under the job lock. State reasons
// are taken from args (the fsm event arguments), falling back to fallback
// when args carry none; when both are empty the current reasons are kept.
// Reaching a terminal state records the completion time.
func (j *Job) setState(state JobState, args []any, fallback ...JobStateReason) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = state
	if reasons := reasonsFromArgs(args...); len(reasons) > 0 {
		j.StateReasons = reasons
	} else if len(fallback) > 0 {
		j.StateReasons = fallback
	}
	if isCompletedState(state) {
		j.Completed = time.Now()
	}
}

func reasonsFromArgs(args ...any) []JobStateReason {
	reasons := make([]JobStateReason, 0, len(args))
	for _, arg := range args {
		if reason, ok := arg.(JobStateReason); ok {
			reasons = append(reasons, reason)
		} else {
			slog.Warn("Invalid argument for job state reason", "arg", arg)
		}
	}
	return reasons
}

// attributes returns job attributes as described in Table 8 in
// https://datatracker.ietf.org/doc/html/rfc2911#section-4.3 , and
// https://datatracker.ietf.org/doc/html/rfc3380
func (j *Job) attributes() goipp.Attributes {
	j.mu.RLock()
	defer j.mu.RUnlock()

	attrs := goipp.Attributes{}
	a := adder(&attrs)
	upTime := j.Printer.UpTime()
	now := time.Now()
	// time-at-* are integers in seconds since printer power-up, dateTime
	// values go into the date-time-at-* counterparts.
	// https://datatracker.ietf.org/doc/html/rfc8011#section-5.3.14
	addTime := func(event string, t time.Time) {
		if t.IsZero() {
			a("time-at-"+event, goipp.TagNoValue, goipp.Void{})
			a("date-time-at-"+event, goipp.TagNoValue, goipp.Void{})
			return
		}
		secs := max(upTime-int(now.Sub(t).Seconds()), 0)
		a("time-at-"+event, goipp.TagInteger, goipp.Integer(secs))
		a("date-time-at-"+event, goipp.TagDateTime, goipp.Time{Time: t})
	}

	a("job-id", goipp.TagInteger, goipp.Integer(j.ID))
	a("job-name", goipp.TagName, goipp.String(j.Name))
	a("job-uri", goipp.TagURI, goipp.String(j.JobURI))
	a("job-state", goipp.TagEnum, goipp.Integer(j.State))
	a("job-state-reasons", goipp.TagKeyword, stringsToValues(j.StateReasons)...)
	a("job-printer-uri", goipp.TagURI, goipp.String(j.PrinterURI))
	a("job-originating-user-name", goipp.TagName, goipp.String(j.Username))
	addTime("creation", j.Created)
	addTime("processing", j.Processing)
	addTime("completed", j.Completed)                                 // https://datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.3
	a("job-printer-up-time", goipp.TagInteger, goipp.Integer(upTime)) // https: //datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.4
	return attrs
}

func (j *Job) IsCompleted() bool {
	return isCompletedState(j.state())
}

func (j *Job) IsActive() bool {
	state := j.state()
	return !isCompletedState(state) && state != JobPending
}

func (j *Job) state() JobState {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.State
}

func isCompletedState(state JobState) bool {
	return state == JobCompleted || state == JobCancelled || state == JobAborted
}
