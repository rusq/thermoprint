package ippsrv

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/looplab/fsm"
)

type Job struct {
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

	sm     *fsm.FSM
	buffer []byte // Buffer for job data, if needed
}

type JobID int32

// JobState represents the state of a job.
// https://datatracker.ietf.org/doc/html/rfc2911#section-4.3.7
//
//go:generate stringer -trimprefix Job -type JobState
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

	jobURL := path.Join(baseURL, p.Name(), fmt.Sprintf("%d", id))

	return createJob(p, id, printerURI.String(), jobURL, jobName.String(), username.String())
}

func createJob(p Printer, id JobID, printerURI, jobURL, name, username string) (*Job, error) {
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
				j.State = JobPendingHeld
				if len(e.Args) > 0 {
					j.StateReasons = reasonsFromArgs(e.Args...)
				} else {
					j.StateReasons = []JobStateReason{JSRJobHeldUntilSpecified}
				}
			},
			jobEvtResume: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job resumed")
				j.State = JobPending
			},
			jobEvtProcess: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job processing started")

				j.State = JobProcessing
				j.StateReasons = []JobStateReason{JSRJobPrinting, JSRJobTransforming}

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
					j.buffer = data // Store the data in the job buffer, for potential reprocessing

				}

				j.Printer.SetState(PSProcessing) // Set the printer state to processing
				j.Processing = time.Now()        // Set the processing time to now
				// Call the printer's Print method with the job data
				if err := j.Printer.Print(ctx, data); err != nil {
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
				j.buffer = nil             // Clear the job buffer after processing

				// Trigger job completion event
				if err := e.FSM.Event(ctx, jobEvtComplete); err != nil {
					lg.ErrorContext(ctx, "Failed to send job completion event", "error", err)
				}
			},
			jobEvtStop: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job processing stopped")
				j.State = JobProcessingStopped
				if len(e.Args) > 0 {
					j.StateReasons = reasonsFromArgs(e.Args...)
				} else {
					j.StateReasons = []JobStateReason{JSRProcessingToStopPoint}
				}
			},
			jobEvtAbort: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job aborted")
				j.State = JobAborted
				if len(e.Args) > 0 {
					j.StateReasons = reasonsFromArgs(e.Args...)
				} else {
					j.StateReasons = []JobStateReason{JSRAbortedBySystem}
				}
			},
			jobEvtComplete: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job completed")
				j.State = JobCompleted
				j.StateReasons = []JobStateReason{JSRJobCompletedSuccessfully}
				j.Completed = time.Now() // Set the completion time to now
			},
			jobEvtCancel: func(ctx context.Context, e *fsm.Event) {
				lg.InfoContext(ctx, "Job cancelled")
				j.State = JobCancelled
				if len(e.Args) > 0 {
					j.StateReasons = reasonsFromArgs(e.Args...)
				} else {
					j.StateReasons = []JobStateReason{JSRJobCancelledByUser}
				}
			},
		},
	)
}

func reasonsFromArgs(args ...interface{}) []JobStateReason {
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
	noValue := goipp.String("no-value")

	nulltime := func(t time.Time) goipp.Value {
		if t.IsZero() {
			return noValue
		}
		return goipp.Integer(int32(t.Unix()))
	}

	b := baseResponse(scSuccessful)
	a := adder(b.Operation)
	a("job-id", goipp.TagInteger, goipp.Integer(j.ID))
	a("job-name", goipp.TagName, goipp.String(j.Name))
	a("job-uri", goipp.TagURI, goipp.String(j.JobURI))
	a("job-state", goipp.TagEnum, goipp.Integer(j.State))
	a("job-state-reasons", goipp.TagKeyword, j.reasons()...)
	a("job-printer-uri", goipp.TagURI, goipp.String(j.PrinterURI))
	a("job-originating-user-name", goipp.TagName, goipp.String(j.Username))
	a("time-at-creation", goipp.TagDateTime, nulltime(j.Created))
	a("time-at-processing", goipp.TagDateTime, nulltime(j.Processing))
	a("time-at-completed", goipp.TagDateTime, nulltime(j.Completed))              // https://datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.3
	a("job-printer-up-time", goipp.TagInteger, goipp.Integer(j.Printer.UpTime())) // https: //datatracker.ietf.org/doc/html/rfc2911#section-4.3.14.4
	return b.Operation
}

func (j *Job) reasons() []goipp.Value {
	return stringsToValues(j.StateReasons)
}

func (j *Job) IsCompleted() bool {
	return j.State == JobCompleted || j.State == JobCancelled || j.State == JobAborted
}

func (j *Job) IsActive() bool {
	return !j.IsCompleted() && j.State != JobPending
}
