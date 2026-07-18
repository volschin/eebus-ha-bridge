package main

import (
	"errors"
	"fmt"
	"sync"
)

var errLifecycleStopping = errors.New("application lifecycle is stopping")

type startedLifecycleStage struct {
	name string
	stop func() error
}

// lifecycleTransaction is the startup commit ledger. A stage is recorded only
// after it starts successfully. Rollback closes the ledger atomically and runs
// the recorded stops once, in exact reverse order. If a concurrent Stop closes
// the ledger while a start call is in flight, add stops that just-started stage
// immediately instead of leaking it.
type lifecycleTransaction struct {
	mu     sync.Mutex
	stages []startedLifecycleStage
	closed bool
	once   sync.Once
	done   chan struct{}
	err    error
}

func newLifecycleTransaction() *lifecycleTransaction {
	return &lifecycleTransaction{done: make(chan struct{})}
}

func (t *lifecycleTransaction) add(name string, stop func() error) error {
	if stop == nil {
		stop = func() error { return nil }
	}
	t.mu.Lock()
	if !t.closed {
		t.stages = append(t.stages, startedLifecycleStage{name: name, stop: stop})
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	if err := stop(); err != nil {
		return errors.Join(errLifecycleStopping, fmt.Errorf("stopping concurrently started %s: %w", name, err))
	}
	return errLifecycleStopping
}

func (t *lifecycleTransaction) rollback() error {
	t.once.Do(func() {
		t.mu.Lock()
		t.closed = true
		stages := append([]startedLifecycleStage(nil), t.stages...)
		t.stages = nil
		t.mu.Unlock()

		var rollbackErrors []error
		for index := len(stages) - 1; index >= 0; index-- {
			stage := stages[index]
			if err := stage.stop(); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("stopping %s: %w", stage.name, err))
			}
		}
		t.err = errors.Join(rollbackErrors...)
		close(t.done)
	})
	<-t.done
	return t.err
}
