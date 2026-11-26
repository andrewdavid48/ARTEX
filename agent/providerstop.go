package agent

import (
	"context"
	"sync"

	"github.com/Autumn-27/artex/llmpool"
	"github.com/Autumn-27/artex/llmrec"
)

// IsRefusalStop reports whether a provider stop reason means the provider's
// safety system declined the request rather than the model ending its turn
// (see llmpool.IsRefusalStop).
func IsRefusalStop(stopReason string) bool { return llmpool.IsRefusalStop(stopReason) }

// stopTracker keeps the stop reason of the latest model call of one run. The
// harness does not expose it: a refused turn has no text and no tool calls, so it
// ends as ReasonCompleted, the same as a model that chose to stop.
type stopTracker struct {
	mu   sync.Mutex
	last string
}

func (t *stopTracker) set(stopReason string) {
	t.mu.Lock()
	t.last = stopReason
	t.mu.Unlock()
}

func (t *stopTracker) get() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

type stopTrackerKey struct{}

// WithStopTracker attaches a fresh stop tracker to ctx and subscribes it to the
// LLM recorder's stop sink, so every model call made under the returned context
// updates it. captureRunSession installs one per run; a nested run gets its own.
func WithStopTracker(ctx context.Context) context.Context {
	t := &stopTracker{}
	ctx = context.WithValue(ctx, stopTrackerKey{}, t)
	return llmrec.WithStopSink(ctx, t.set)
}

type runStopObserverKey struct{}

// WithRunStopObserver lets a caller learn how the next run(s) it starts under
// ctx ended: each top-level captureRunSession reports the stop reason of its
// last model call here when it returns (RunStopReason). Nested runs do not see
// the observer, so a subagent's refusal never stands in for the run's own.
func WithRunStopObserver(ctx context.Context) context.Context {
	return context.WithValue(ctx, runStopObserverKey{}, &stopTracker{})
}

// RunStopReason returns the stop reason of the last model call of the latest
// run that finished under ctx's observer, or "" when there is none.
func RunStopReason(ctx context.Context) string {
	if t, _ := ctx.Value(runStopObserverKey{}).(*stopTracker); t != nil {
		return t.get()
	}
	return ""
}

// takeRunStopObserver returns ctx's run observer and a ctx without it, so the
// run reports to it and the runs nested inside do not.
func takeRunStopObserver(ctx context.Context) (*stopTracker, context.Context) {
	t, _ := ctx.Value(runStopObserverKey{}).(*stopTracker)
	if t == nil {
		return nil, ctx
	}
	return t, context.WithValue(ctx, runStopObserverKey{}, (*stopTracker)(nil))
}

// LastStopReason returns the stop reason of the latest model call made under ctx
// by the run that owns it, or "" when none was observed.
func LastStopReason(ctx context.Context) string {
	if t, _ := ctx.Value(stopTrackerKey{}).(*stopTracker); t != nil {
		return t.get()
	}
	return ""
}
