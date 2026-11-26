package llmpool

import (
	"context"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

const googleBlockText = "The prompt could not be submitted. The prompt contains sensitive words that violate Google's Generative AI Prohibited Use policy."

func streamOf(evs ...llm.StreamEvent) *fakeProv {
	return &fakeProv{events: [][]llm.StreamEvent{evs}}
}

func collect(t *testing.T, p llm.Provider) (*llm.Accumulator, []llm.StreamEvent) {
	t.Helper()
	acc := llm.NewAccumulator()
	var evs []llm.StreamEvent
	for ev, err := range p.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(ev)
		evs = append(evs, ev)
	}
	return acc, evs
}

func TestBlockedTextBecomesContentFilter(t *testing.T) {
	for name, chunks := range map[string][]string{
		"one delta":    {googleBlockText},
		"split deltas": {"The pro", "mpt could not be sub", "mitted. The prompt contains sensitive words."},
	} {
		evs := []llm.StreamEvent{{Type: llm.SEMessageStart}}
		for _, c := range chunks {
			evs = append(evs, llm.StreamEvent{Type: llm.SETextDelta, Text: c})
		}
		evs = append(evs, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
		acc, _ := collect(t, WrapBlockedText(streamOf(evs...)))
		if acc.StopReason != "content_filter" || acc.Message().Text() != "" {
			t.Fatalf("%s: stop=%q text=%q, want content_filter and no text", name, acc.StopReason, acc.Message().Text())
		}
	}
}

func TestOrdinaryTextPassesThrough(t *testing.T) {
	for _, text := range []string{"The prompt was fine.", "Here is the report.", "The"} {
		acc, _ := collect(t, WrapBlockedText(streamOf(
			llm.StreamEvent{Type: llm.SETextDelta, Text: text},
			llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"},
		)))
		if acc.StopReason != "end_turn" || acc.Message().Text() != text {
			t.Fatalf("%q: stop=%q text=%q", text, acc.StopReason, acc.Message().Text())
		}
	}
}

func TestBlockedTextCompleteIsContentFilter(t *testing.T) {
	p := WrapBlockedText(okProv("g", googleBlockText))
	msg, sr, _, err := p.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil || sr != "content_filter" || msg.Text() != "" {
		t.Fatalf("stop=%q text=%q err=%v", sr, msg.Text(), err)
	}
}

// End to end with the pool: a member that answers with Google's block text falls
// over to the next member.
func TestPoolFallsOverOnBlockedText(t *testing.T) {
	g := WrapBlockedText(streamOf(llm.StreamEvent{Type: llm.SETextDelta, Text: googleBlockText}))
	b := okProv("b", "report")
	p := New([]*Member{member(1, "g", 10, g), member(2, "b", 5, b)}, NewRegistry(nil, nil))
	got, err := drain(p.Stream(context.Background(), llm.CompletionRequest{}))
	if err != nil || got != "report" || b.calls != 1 {
		t.Fatalf("text=%q err=%v backup calls=%d", got, err, b.calls)
	}
}
