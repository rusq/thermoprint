package ippsrv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const jobRetention = 24 * time.Hour // Duration to retain job files in the spool

type spooler interface {
	AddJob(ctx context.Context, job *Job, data []byte) error
	RemoveJob(jobID JobID) error
	GetJob(jobID JobID) (*Job, error)
	// GetJobs returns all jobs for a specific printer by its ID.
	GetJobs(prnID string) ([]*Job, error) // code 10
	GetJobData(jobID JobID) ([]byte, error)
	GetJobCount(prnID string) int
	ListJobs() ([]*Job, error)
	io.Closer
}

type spool struct {
	dir  string        // Directory where jobs are spooled
	msgC chan spoolmsg // Channel for spool messages

	mu          sync.Mutex         // Mutex to protect concurrent access
	jobs        map[JobID]*Job     // In-memory cache of jobs, keyed by JobID
	printerJobs map[string][]JobID // Jobs per printer, keyed by printer ID
}

type spoolmsg struct {
	command int
}

func newSpool(spoolDir string) (*spool, error) {
	if spoolDir == "" {
		var err error
		spoolDir, err = os.MkdirTemp("", "ipp-spool")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary spool directory: %w", err)
		}
		slog.Info("using temporary spool directory", "dir", spoolDir)
	} else {
		slog.Info("using specified spool directory", "dir", spoolDir)
		if err := os.MkdirAll(spoolDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create spool directory %s: %w", spoolDir, err)
		}
	}
	sp := &spool{
		dir:         spoolDir,
		jobs:        make(map[JobID]*Job),
		printerJobs: make(map[string][]JobID),
		msgC:        make(chan spoolmsg, 100), // Buffered channel for spool messages
	}
	go sp.worker()
	return sp, nil
}

func (s *spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	slog.Debug("closing spool", "dir", s.dir)
	close(s.msgC)
	if err := os.RemoveAll(s.dir); err != nil {
		return fmt.Errorf("failed to remove spool directory %s: %w", s.dir, err)
	}
	slog.Info("spool closed", "dir", s.dir)
	return nil
}

func (s *spool) worker() {
	slog.Info("spool worker started", "dir", s.dir)
	ticker := time.NewTicker(10 * time.Second) // Adjust the interval as needed
	defer ticker.Stop()

	for {
		select {
		case msg, more := <-s.msgC:
			if !more {
				slog.Info("spool worker stopping, channel closed")
				return
			}
			_ = msg // Process the message if needed
		case <-ticker.C:
			s.mu.Lock()
			activeJobCount := 0
			for _, job := range s.jobs {
				if job.IsActive() {
					activeJobCount++
				}
			}
			if activeJobCount > 0 {
				slog.Info("spool worker running", "job_count", activeJobCount)
			}
			s.pruneLocked()
			s.mu.Unlock()
		}
	}
}

var (
	errJobAlreadyExists = errors.New("job already exists")
	errJobNotFound      = errors.New("job not found")
)

func (s *spool) pruneLocked() {
	for jobID, job := range s.jobs {
		if time.Since(job.Created) > jobRetention && job.IsCompleted() {
			slog.Info("removing old job", "job_id", jobID, "created_at", job.Created)
			if err := s.removeJobLocked(jobID); err != nil {
				slog.Error("failed to remove old job", "job_id", jobID, "error", err)
			}
		}
	}
}

func (s *spool) addJobLocked(job *Job) error {
	if _, ok := s.jobs[job.ID]; ok {
		return errJobAlreadyExists
	}

	s.jobs[job.ID] = job
	pjobs := s.printerJobs[job.Printer.Name()]
	if slices.Contains(pjobs, job.ID) {
		return fmt.Errorf("job %d already exists for printer %s", job.ID, job.Printer.Name())
	}
	// Add the job ID to the printer's job list
	if s.printerJobs[job.Printer.Name()] == nil {
		s.printerJobs[job.Printer.Name()] = make([]JobID, 0)
	}
	s.printerJobs[job.Printer.Name()] = append(s.printerJobs[job.Printer.Name()], job.ID)
	return nil
}

func (s *spool) removeJobLocked(jobID JobID) error {
	job, ok := s.jobs[jobID]
	if !ok {
		return errJobNotFound
	}

	filePath := s.jobFilePath(jobID)
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to remove job file %s: %w", filePath, err)
	}

	delete(s.jobs, jobID)

	// Remove the job ID from the printer's job list
	printerJobs := s.printerJobs[job.Printer.Name()]
	for i, existingJobID := range printerJobs {
		if existingJobID == jobID {
			printerJobs = append(printerJobs[:i], printerJobs[i+1:]...)
			s.printerJobs[job.Printer.Name()] = printerJobs
			break
		}
	}
	return nil
}

func (s *spool) AddJob(ctx context.Context, job *Job, data []byte) error {
	if job == nil {
		return errors.New("job cannot be nil")
	}
	if job.Printer == nil {
		return errors.New("job printer cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.addJobLocked(job); err != nil {
		return fmt.Errorf("failed to add job %d: %w", job.ID, err)
	}

	jobFile := s.jobFilePath(job.ID)
	f, err := os.Create(jobFile)
	if err != nil {
		return fmt.Errorf("failed to create job file %s: %w", jobFile, err)
	}
	defer f.Close()
	// Write the job data to the file
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write job data to file %s: %w", jobFile, err)
	}
	slog.Info("job added", "job_id", job.ID, "printer", job.Printer.Name(), "file", jobFile)

	return job.sm.Event(ctx, jobEvtProcess, data)
}

func (s *spool) jobFilePath(jobID JobID) string {
	return filepath.Join(s.dir, fmt.Sprintf("job_%d.ps", jobID))
}

func (s *spool) RemoveJob(jobID JobID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.removeJobLocked(jobID); err != nil {
		return fmt.Errorf("failed to remove job %d: %w", jobID, err)
	}
	// Remove the job file from the spool directory

	jobFile := s.jobFilePath(jobID)
	if err := os.Remove(s.jobFilePath(jobID)); err != nil {
		return fmt.Errorf("failed to remove job file %s: %w", jobFile, err)
	}
	return nil
}

func (s *spool) GetJob(jobID JobID) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, errJobNotFound
	}
	return job, nil
}

func (s *spool) GetJobData(jobID JobID) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return nil, errJobNotFound
	}
	jobFile := s.jobFilePath(job.ID)
	data, err := os.ReadFile(jobFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read job file %s: %w", jobFile, err)
	}
	return data, nil
}

func (s *spool) ListJobs() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobList := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobList = append(jobList, job)
	}
	if len(jobList) == 0 {
		return nil, errJobNotFound
	}
	return jobList, nil
}

func (s *spool) GetJobCount(prnID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if jobs, ok := s.printerJobs[prnID]; ok {
		return len(jobs)
	}
	return 0
}

func (s *spool) GetJobs(prnID string) ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobIDs, ok := s.printerJobs[prnID]
	if !ok {
		return nil, fmt.Errorf("no jobs found for printer %s", prnID)
	}

	jobs := make([]*Job, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		job, ok := s.jobs[jobID]
		if !ok {
			continue // Skip if the job is not found
		}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return nil, errJobNotFound
	}
	return jobs, nil
}
