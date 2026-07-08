package tripcdp

import (
	"sync"
	"time"
)

type workerProgressWatchdog struct {
	mu       sync.Mutex
	timeout  time.Duration
	last     time.Time
	stopped  chan struct{}
	reporter Reporter
	vendor   string
	workerID int
	cancel   func()
}

func newWorkerProgressWatchdog(timeout time.Duration, reporter Reporter, vendor string, workerID int, cancel func()) *workerProgressWatchdog {
	w := &workerProgressWatchdog{timeout: timeout, last: time.Now(), stopped: make(chan struct{}), reporter: reporter, vendor: vendor, workerID: workerID, cancel: cancel}
	if timeout <= 0 || cancel == nil {
		return w
	}
	go w.loop()
	return w
}

func (w *workerProgressWatchdog) mark(event string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.last = time.Now()
	w.mu.Unlock()
}

func (w *workerProgressWatchdog) stop() {
	if w == nil {
		return
	}
	select {
	case <-w.stopped:
	default:
		close(w.stopped)
	}
}

func (w *workerProgressWatchdog) loop() {
	ticker := time.NewTicker(maxDuration(w.timeout/4, 10*time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-w.stopped:
			return
		case <-ticker.C:
			w.mu.Lock()
			stalled := time.Since(w.last) >= w.timeout
			w.mu.Unlock()
			if stalled {
				if w.reporter != nil {
					w.reporter(Event{"event": "worker_stalled", "stage": "watchdog", "vendor": w.vendor, "worker_id": w.workerID, "failure_class": FailureWorkerStalled, "stall_timeout_ms": w.timeout.Milliseconds()})
				}
				w.cancel()
				return
			}
		}
	}
}

func maxDuration(a time.Duration, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
