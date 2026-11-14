package main

import "sync"

// awake holds a system idle-sleep prevention while the settings ask for it
// and an agent works, and releases it otherwise.
type awake struct {
	mu      sync.Mutex
	working bool
	display bool
	release func()
}

func (w *awake) update(keep, keepDisplay, working bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.working = working
	want := (keep || keepDisplay) && working
	display := want && keepDisplay
	if w.release != nil && (!want || display != w.display) {
		w.release()
		w.release = nil
	}
	if want && w.release == nil {
		w.release = keepAwake("pigo agents are working", display)
		w.display = display
	}
}

func (w *awake) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.release != nil {
		w.release()
		w.release = nil
	}
}
