package ippsrv

import (
	"errors"
	"sort"
	"time"
)

// ServerSnapshot is a stable, read-only copy of server, printer, and spool
// state for observers such as terminal dashboards.
type ServerSnapshot struct {
	BaseURL         string
	ListenAddr      string
	Uptime          time.Duration
	Debug           bool
	DumpDir         string
	BonjourEnabled  bool
	MaxDocumentSize int64
	Printers        []PrinterSnapshot
	Jobs            []JobSnapshot
}

// PrinterSnapshot is a stable copy of an IPP printer's public state.
type PrinterSnapshot struct {
	Name         string
	MakeAndModel string
	Info         string
	State        PrinterState
	Ready        bool
	UpTime       int
	MediaDefault string
	UUID         string
}

// JobSnapshot is a stable copy of a spooled job.
type JobSnapshot struct {
	ID           JobID
	PrinterName  string
	State        JobState
	StateReasons []JobStateReason
	Name         string
	Created      time.Time
	Processing   time.Time
	Completed    time.Time
	Username     string
	JobURI       string
	PrinterURI   string
	Format       string
}

// Snapshot returns a stable copy of the server state.
func (s *Server) Snapshot() ServerSnapshot {
	snap := ServerSnapshot{
		Uptime:          time.Since(startTime),
		Debug:           s.debug,
		DumpDir:         s.dumpdir,
		BonjourEnabled:  s.bonjour.enabled,
		MaxDocumentSize: MaxDocumentSize,
	}
	if s.srv != nil {
		snap.ListenAddr = s.srv.Addr
	}
	if s.is != nil {
		snap.BaseURL = s.is.baseURL
	}
	for _, p := range s.pp {
		snap.Printers = append(snap.Printers, snapshotPrinter(p))
	}
	if s.is != nil && s.is.spool != nil {
		jobs, err := s.is.spool.ListJobs()
		if err == nil {
			for _, job := range jobs {
				snap.Jobs = append(snap.Jobs, job.Snapshot())
			}
		} else if !errors.Is(err, errJobNotFound) {
			snap.Jobs = nil
		}
	}
	sort.Slice(snap.Jobs, func(i, j int) bool {
		return snap.Jobs[i].ID < snap.Jobs[j].ID
	})
	return snap
}

func snapshotPrinter(p Printer) PrinterSnapshot {
	return PrinterSnapshot{
		Name:         p.Name(),
		MakeAndModel: p.MakeAndModel(),
		Info:         p.Info(),
		State:        p.State(),
		Ready:        p.Ready(),
		UpTime:       p.UpTime(),
		MediaDefault: p.MediaDefault(),
		UUID:         p.UUID(),
	}
}

// Snapshot returns a stable copy of the job without exposing mutable slices.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var printerName string
	if j.Printer != nil {
		printerName = j.Printer.Name()
	}
	reasons := make([]JobStateReason, len(j.StateReasons))
	copy(reasons, j.StateReasons)
	return JobSnapshot{
		ID:           j.ID,
		PrinterName:  printerName,
		State:        j.State,
		StateReasons: reasons,
		Name:         j.Name,
		Created:      j.Created,
		Processing:   j.Processing,
		Completed:    j.Completed,
		Username:     j.Username,
		JobURI:       j.JobURI,
		PrinterURI:   j.PrinterURI,
		Format:       j.Format,
	}
}
