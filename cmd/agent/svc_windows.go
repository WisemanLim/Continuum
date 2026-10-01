//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

// underSCM runs fn as a Windows service when started by the Service Control Manager (FR-N04);
// Stop/Shutdown cancel the context. Returns false when started from a console.
func underSCM(fn func(ctx context.Context) error) (bool, error) {
	ok, err := svc.IsWindowsService()
	if err != nil || !ok {
		return false, err
	}
	return true, svc.Run("ContinuumAgent", handler{fn})
}

type handler struct {
	fn func(ctx context.Context) error
}

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.fn(ctx) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case <-done:
			cancel()
			return false, 1
		}
	}
}
