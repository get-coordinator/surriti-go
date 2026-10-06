package surriti

import (
	"context"
	"sync"
	"testing"
	"time"
)

type lifecycleDriver struct {
	Queryer
	close func() error
}

func (d lifecycleDriver) Close(context.Context) error { return d.close() }

func TestSurritiCloseDrainsBeforeClosingDriver(t *testing.T) {
	for _, mode := range []string{"graceful", "cancelled", "grace-expired"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseWorker()
			started, cancelled, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			driverClosed := make(chan struct{}, 2)
			driver := lifecycleDriver{
				Queryer: queryFunc(func(context.Context, string, map[string]any) (any, error) { return nil, nil }),
				close: func() error {
					select {
					case <-finished:
					default:
						t.Error("driver closed before background work finished")
					}
					driverClosed <- struct{}{}
					return nil
				},
			}
			off := false
			memory, err := NewSurriti(driver, &SurritiOptions{CognitionEnabled: &off})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = memory.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			memory.runBackground(func(ctx context.Context) {
				close(started)
				select {
				case <-ctx.Done():
					close(cancelled)
					<-release // Simulate cleanup that must finish before transport teardown.
				case <-release:
				}
				close(finished)
			})
			<-started
			closeCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "graceful" {
				releaseWorker()
			}
			closed := make(chan error, 1)
			go func() { closed <- memory.Close(closeCtx) }()
			if mode != "graceful" {
				select {
				case <-cancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("background work was not cancelled")
				}
				select {
				case <-driverClosed:
					t.Fatal("driver closed while worker was cleaning up")
				default:
				}
				select {
				case <-closed:
					t.Fatal("Close returned while worker was cleaning up")
				default:
				}
				releaseWorker()
			}
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not finish")
			}
			<-driverClosed
			// A closed instance rejects jobs; reconnect gives new jobs a live context.
			memory.runBackground(func(context.Context) { t.Error("closed instance accepted work") })
			if _, err = memory.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			ran := make(chan error, 1)
			memory.runBackground(func(ctx context.Context) { ran <- ctx.Err() })
			select {
			case err := <-ran:
				if err != nil {
					t.Fatalf("reconnect reused cancelled context: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("reconnected instance rejected work")
			}
			if err := memory.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
