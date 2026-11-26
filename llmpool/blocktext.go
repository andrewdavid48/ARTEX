package llmpool

import (
	"context"
	"iter"
	"strings"

	"github.com/Autumn-27/norma/llm"
)

// googleBlockMarker opens the text Google's Gemini endpoints return instead of a
// refusal stop reason when a prompt trips their Prohibited Use filter: "The prompt
// could not be submitted. The prompt contains sensitive words that violate
// Google's Generative AI Prohibited Use policy. ..." The turn otherwise looks like
// an ordinary answer (stop_reason end_turn).
const googleBlockMarker = "The prompt could not be submitted"

// WrapBlockedText makes a provider report a policy block that arrives as answer
// text the way a refusal arrives: no text and stop reason "content_filter". Done
// once where providers are built, so failover, the task chain and intent settling
// treat it like any other refusal.
func WrapBlockedText(p llm.Provider) llm.Provider {
	if p == nil {
		return nil
	}
	return blockedTextProvider{inner: p}
}

type blockedTextProvider struct{ inner llm.Provider }

func (b blockedTextProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	msg, sr, usage, err := b.inner.Complete(ctx, req)
	if err == nil && len(msg.ToolUses()) == 0 && strings.HasPrefix(strings.TrimSpace(msg.Text()), googleBlockMarker) {
		return llm.Message{Role: llm.RoleAssistant}, "content_filter", usage, nil
	}
	return msg, sr, usage, err
}

func (b blockedTextProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		var (
			held      []llm.StreamEvent // leading text deltas, until they can be told from the marker
			acc       string
			decided   bool // the first text is known to be the marker or not
			blocked   bool
			deltaSent bool
		)
		flush := func() bool {
			for _, ev := range held {
				if !yield(ev, nil) {
					return false
				}
			}
			held = nil
			return true
		}
		blockDelta := func(usage llm.Usage) bool {
			deltaSent = true
			return yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "content_filter", Usage: usage}, nil)
		}
		for ev, err := range b.inner.Stream(ctx, req) {
			if err != nil {
				if !decided && !flush() {
					return
				}
				yield(ev, err)
				return
			}
			if blocked {
				switch ev.Type {
				case llm.SEMessageDelta:
					if !blockDelta(ev.Usage) {
						return
					}
				case llm.SEMessageStop:
					if !deltaSent && !blockDelta(llm.Usage{}) {
						return
					}
					if !yield(ev, nil) {
						return
					}
				case llm.SEMessageStart:
					if !yield(ev, nil) { // usage only
						return
					}
				}
				continue // the block text and anything else of the turn is dropped
			}
			if !decided {
				if ev.Type == llm.SETextDelta {
					held = append(held, ev)
					acc += ev.Text
					lead := strings.TrimLeft(acc, " \n\r\t")
					switch {
					case strings.HasPrefix(lead, googleBlockMarker):
						decided, blocked, held = true, true, nil
					case !strings.HasPrefix(googleBlockMarker, lead):
						decided = true
						if !flush() {
							return
						}
					}
					continue
				}
				if CommitsOutput(ev) { // a tool call or the end of the turn settles it: not the marker
					decided = true
					if !flush() {
						return
					}
				}
			}
			if !yield(ev, nil) {
				return
			}
		}
		if blocked && !deltaSent {
			blockDelta(llm.Usage{})
			return
		}
		if !decided {
			flush()
		}
	}
}
