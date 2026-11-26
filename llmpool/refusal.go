package llmpool

import "github.com/Autumn-27/norma/llm"

// IsRefusalStop reports whether a provider stop reason means the provider's
// safety system declined the request rather than the model ending its turn.
// Anthropic returns stop_reason "refusal" with an empty or partial message;
// OpenAI-compatible endpoints return finish_reason "content_filter", which Norma
// passes through unmapped.
func IsRefusalStop(stopReason string) bool {
	return stopReason == "refusal" || stopReason == "content_filter"
}

// CommitsOutput reports whether delivering ev hands the caller model output, after
// which the attempt can no longer be replayed on another provider. Text, tool calls
// and the closing message_delta/message_stop commit. Thinking does not: a turn that
// so far only thought can still be dropped (on a transient failure or a provider
// refusal) and replayed, since the harness has not seen any of it yet.
func CommitsOutput(ev llm.StreamEvent) bool {
	switch ev.Type {
	case llm.SETextDelta, llm.SEToolInputJSON, llm.SEToolUseSignature:
		return ev.Text != ""
	case llm.SEToolUseStart, llm.SEMessageDelta, llm.SEMessageStop:
		return true
	default:
		return false
	}
}

// IsRefusalDelta reports whether ev is the message_delta of a refused turn.
func IsRefusalDelta(ev llm.StreamEvent) bool {
	return ev.Type == llm.SEMessageDelta && IsRefusalStop(ev.StopReason)
}

// IsEmptyRefusal reports whether a non-streaming result is a refused turn that
// produced no text and no tool calls, which is safe to replay on the next member.
func IsEmptyRefusal(msg llm.Message, stopReason string) bool {
	return IsRefusalStop(stopReason) && msg.Text() == "" && len(msg.ToolUses()) == 0
}
