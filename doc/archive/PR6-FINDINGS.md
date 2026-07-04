# Review Findings for PR #6

Findings from the code review of PR #6 ("Log IPP encode failures"), ranked most-severe first.

## [DONE] [P1] Get-Jobs on an empty queue returns an IPP error instead of successful-ok

`spool.GetJobs` returns a bespoke `fmt.Errorf("no jobs found for printer %s")` for a never-spooled printer and `errJobNotFound` for an emptied queue; `ippStatusFromError` turns those into `server-error-internal-error` and `client-error-not-found` respectively. A client that adds the printer and polls Get-Jobs before printing anything gets a failure — and a different failure depending on history — where RFC 8011 section 4.2.6.2 requires `successful-ok` with zero job groups.

Proposed fix: treat "no jobs" as an empty slice, not an error.

References: `ippsrv/ipp.go` (`handleGetJobs`, `ippStatusFromError`), `ippsrv/spool.go` (`GetJobs`).

## [DONE] [P1] time-at-* job attributes use dateTime syntax; RFC defines them as integers

`addTime` in `Job.attributes` emits `goipp.TagDateTime` + `goipp.Time` for `time-at-creation`/`time-at-processing`/`time-at-completed`, but RFC 8011 section 5.3.14 defines `time-at-*` as `integer` (seconds since printer power-up); the dateTime-syntax versions are the separate `date-time-at-*` attributes. Conforming clients parsing `time-at-*` as integer get an out-of-band type.

Proposed fix: emit `time-at-*` as integer seconds since power-up (derive from the printer's up-time clock), and emit the dateTime values under the `date-time-at-*` names.

Reference: `ippsrv/job.go` (`attributes`).

## [DONE] [P1] Failed spool-file write leaves a phantom job that can never be removed

`spool.AddJob` registers the job in the maps before `os.Create`/`Write`/`Close`; on failure the job stays registered with no file. `removeJobLocked` returns on the `os.Remove` error — `ErrNotExist` is not tolerated — before deleting the map entries, so the job is permanently stuck and inflates `GetJobCount`.

Proposed fix: roll back the registration when persisting the job file fails, and make `removeJobLocked` tolerate `os.ErrNotExist` and clean the maps regardless.

Reference: `ippsrv/spool.go` (`AddJob`, `removeJobLocked`).

## [DONE] [P2] Printer job-lock fallback can panic on hash of unhashable type

`printerJobLockKeyFor` uses `reflect.Type.Comparable()`, a static check: a value-type `Printer` with an interface field whose dynamic value is a map/slice/func passes it, then panics with "hash of unhashable type" at `printerJobLocks.LoadOrStore` in the FSM print path. Only reachable via external printers registered through `WithAdditionalPrinters`.

Reference: `ippsrv/job.go` (`printerJobLockKeyFor`).

## [DONE] [P2] Fallback printer-lock registry leaks entries and has weak identity

`printerJobLocks` entries are never deleted (one mutex per printer instance, for the process lifetime), pointer keys use a raw `uintptr` that GC address reuse can alias to an unrelated printer, and the non-comparable fallback keys by `p.Name()` — reinstating the same-name lock sharing that commit 535e98c removed, for that class of printer.

Proposed fix (also covers the previous item): make job serialization part of the `Printer` contract, or lock per printer in the spool, instead of a reflect-keyed global registry.

Reference: `ippsrv/job.go` (`printerJobLocks`, `lockPrinterJob`).

## [DONE] [P3] jobMu and printMu both serialize the same print work

The only `Print` caller runs entirely inside the `lockPrinterJob` → `jobMu` critical section, so `printMu` is redundant. Having `lockJob()` lock `printMu` (or dropping `printMu`) leaves one mutex and no lock-ordering question.

Reference: `ippsrv/printer.go` (`basePrinter`).

## [DONE] [P3] Seven FSM callbacks hand-place identical lock/assign/unlock blocks

Each callback repeats `j.mu.Lock()` → assign `State`/`StateReasons` → `j.mu.Unlock()`, and five repeat the same `len(e.Args) > 0 ? reasonsFromArgs : default` pattern. A `setState(state, fallbackReason, args...)` helper collapses ~40 lines and makes it impossible for a future callback to skip the lock; unexporting `State`/`StateReasons` would stop external packages bypassing the mutex.

Reference: `ippsrv/job.go` (`makeJobFSM`).

## [DONE] [P3] Duplicated concurrency-test scaffolding

`blockingDriver` differs from `serialBlockingDriver` only in `close(started)` vs a channel send, and four concurrency tests repeat the same go-AddJob/select-entered-err-timeout blocks (8 near-identical selects). One driver plus `startAddJob`/`waitStarted`/`waitDone` helpers would cut ~100 lines.

Reference: `ippsrv/spool_test.go`.

## Note (not a finding)

Empty `job-state-reasons` would encode as an invalid `""` keyword via `adder`'s empty-value substitution, but no current code path produces an empty reasons slice. Resolved as a side effect of the `setState` helper: when the event args carry no valid reasons it falls back to the event's default reason, so `StateReasons` can no longer become empty through FSM events.
