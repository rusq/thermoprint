package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
)

func trapSigInfo() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINFO, syscall.SIGUSR1)
	go func() {
		for range ch {
			fmt.Fprint(os.Stderr, "THERMOPRINT STATUS REPORT\n")
			cfg.SigInfo(os.Stderr)
		}
	}()
}
