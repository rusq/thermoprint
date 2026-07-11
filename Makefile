SHELL=/bin/sh

tp:
	go build -ldflags="-s -w" ./cmd/tp

debug:
	GOEXPERIMENT=goroutineleakprofile go build -tags=debug ./cmd/tp
.PHONY: debug
