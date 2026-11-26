package server

import (
	"context"
	"iter"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

// refusedTurn is a provider turn the safety system declined after some thinking
// and before any text or tool call.
func refusedTurn(stop string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.SEMessageStart, Usage: llm.Usage{InputTokens: 100}},
		{Type: llm.SEThinkingDelta, Text: "considering"},
		{Type: llm.SEMessageDelta, StopReason: stop, Usage: llm.Usage{InputTokens: 100, OutputTokens: 5}},
		{Type: llm.SEMessageStop},
	}
}

func okTurn(text string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.SETextDelta, Text: text},
		{Type: llm.SEMessageDelta, StopReason: "end_turn"},
		{Type: llm.SEMessageStop},
	}
}

func streamChainForTest(hooks taskLLMStreamHooks) (llm.Message, string, llm.Usage, error) {
	return accumulateStreamForTest(context.Background(),
		func(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
			return streamTaskLLM(ctx, "7", req, hooks)
		}, llm.CompletionRequest{})
}

// refusalHooks runs profile 11 (first) and, once exhausted, profile 22 (second).
func refusalHooks(first, second llm.Provider, hasFallback bool, exhausted *int, health *[]error) taskLLMStreamHooks {
	active := int64(11)
	return taskLLMStreamHooks{
		current: func(context.Context) (taskLLMSelection, error) {
			if active == 11 {
				return taskLLMSelection{profileID: 11, healthProfileID: 11, provider: first}, nil
			}
			return taskLLMSelection{profileID: 22, healthProfileID: 22, provider: second}, nil
		},
		exhaust: func(taskLLMSelection, error) (db.TaskLLMTransition, error) {
			*exhausted++
			active = 22
			next := active
			return db.TaskLLMTransition{PreviousProfileID: 11, NextProfileID: &next, Advanced: true}, nil
		},
		recordHealth: func(_ context.Context, _ int64, _, _ bool, err error) { *health = append(*health, err) },
		hasFallback:  func(taskLLMSelection) bool { return hasFallback },
	}
}

func TestTaskLLMStreamRefusalFallsBackToNextProfile(t *testing.T) {
	for _, stop := range []string{"refusal", "content_filter"} {
		first := &scriptedLLMProvider{events: refusedTurn(stop)}
		second := &scriptedLLMProvider{events: okTurn("fallback")}
		exhausted := 0
		var health []error
		hooks := refusalHooks(first, second, true, &exhausted, &health)

		msg, sr, _, err := streamChainForTest(hooks)
		if err != nil {
			t.Fatalf("%s: %v", stop, err)
		}
		if msg.Text() != "fallback" || sr != "end_turn" {
			t.Fatalf("%s: got text %q stop %q, want the fallback's turn", stop, msg.Text(), sr)
		}
		for _, b := range msg.Content {
			if b.Type == llm.BlockThinking {
				t.Fatalf("%s: refused attempt's thinking leaked into the fallback turn", stop)
			}
		}
		if first.calls != 1 || second.calls != 1 || exhausted != 1 {
			t.Fatalf("%s: calls first=%d second=%d exhausted=%d, want 1/1/1 (no same-profile retry)", stop, first.calls, second.calls, exhausted)
		}
		for _, e := range health {
			if e != nil {
				t.Fatalf("%s: refusal reported to the breaker as a failure: %v", stop, e)
			}
		}
	}
}

func TestTaskLLMStreamRefusalOnLastProfileIsDelivered(t *testing.T) {
	first := &scriptedLLMProvider{events: refusedTurn("refusal")}
	exhausted := 0
	var health []error
	hooks := refusalHooks(first, nil, false, &exhausted, &health)

	_, sr, _, err := streamChainForTest(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if sr != "refusal" || exhausted != 0 {
		t.Fatalf("stop=%q exhausted=%d, want the refusal delivered and the chain untouched", sr, exhausted)
	}
}

func TestTaskLLMStreamRefusalAfterTextIsNotReplayed(t *testing.T) {
	first := &scriptedLLMProvider{events: []llm.StreamEvent{
		{Type: llm.SETextDelta, Text: "partial"},
		{Type: llm.SEMessageDelta, StopReason: "refusal"},
		{Type: llm.SEMessageStop},
	}}
	second := &scriptedLLMProvider{events: okTurn("fallback")}
	exhausted := 0
	var health []error
	hooks := refusalHooks(first, second, true, &exhausted, &health)

	msg, sr, _, err := streamChainForTest(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text() != "partial" || sr != "refusal" || second.calls != 0 {
		t.Fatalf("text=%q stop=%q second=%d, want the delivered refusal kept", msg.Text(), sr, second.calls)
	}
}

func TestTaskLLMCompleteRefusalFallsBackToNextProfile(t *testing.T) {
	first := &scriptedLLMProvider{events: refusedTurn("refusal")}
	second := &scriptedLLMProvider{events: okTurn("fallback")}
	exhausted := 0
	var health []error
	hooks := refusalHooks(first, second, true, &exhausted, &health)

	msg, sr, _, err := completeTaskLLM(context.Background(), "7", llm.CompletionRequest{}, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text() != "fallback" || sr != "end_turn" || first.calls != 1 || exhausted != 1 {
		t.Fatalf("text=%q stop=%q first=%d exhausted=%d", msg.Text(), sr, first.calls, exhausted)
	}
}

func TestChainHasFallback(t *testing.T) {
	chain := []db.TaskLLMProfile{
		{ProfileID: 1, Position: 0, Status: "quota_exhausted"},
		{ProfileID: 2, Position: 1, Status: "ready"},
		{ProfileID: 3, Position: 2, Status: "ready"},
	}
	if !chainHasFallback(chain, 2, false) {
		t.Fatal("profile 3 follows 2")
	}
	if chainHasFallback(chain, 3, false) {
		t.Fatal("3 is last; nothing follows without rotation")
	}
	if !chainHasFallback(chain, 3, true) {
		t.Fatal("rotation wraps to ready profile 2")
	}
}
