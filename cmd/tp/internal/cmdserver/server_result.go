package cmdserver

type serverResult struct {
	done chan struct{}
	err  error
}

func newServerResult() *serverResult {
	return &serverResult{done: make(chan struct{})}
}

func (r *serverResult) finish(err error) {
	r.err = err
	close(r.done)
}

func (r *serverResult) wait() error {
	<-r.done
	return r.err
}
