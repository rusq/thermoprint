package cfg

import "io"

type InfoReportFunc func(w io.Writer)

var sigReporters []InfoReportFunc

func RegisterSigInfoReporter(fn InfoReportFunc) {
	if fn == nil {
		return
	}
	sigReporters = append(sigReporters, fn)
}

func SigInfo(w io.Writer) {
	if w == nil {
		return
	}
	for _, fn := range sigReporters {
		fn(w)
	}
}
