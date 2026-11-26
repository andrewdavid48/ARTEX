package db

import (
	"errors"
	"testing"
)

// The third exhausted settlement of an intent blocks it; a rerun in between
// (ReopenIntent) keeps the count.
func TestSettleIntentClaimExhaustedBlocksOnLimit(t *testing.T) {
	d, a, _ := openClaimInstances(t)
	id := addClaimIntent(t, a)
	for i := 1; i <= IntentExhaustLimit; i++ {
		token := claimIntentToken(t, a, id, "work#1")
		state, err := a.SettleIntentClaimExhausted(id, token)
		if err != nil {
			t.Fatalf("settle %d: %v", i, err)
		}
		row := readClaimRow(t, d, id)
		if i < IntentExhaustLimit {
			if state != "exhausted" || row.state != "exhausted" || row.blockedReason.Valid {
				t.Fatalf("settle %d: state=%q row=%+v, want exhausted", i, state, row)
			}
			if ok, err := a.ReopenIntent(id); err != nil || !ok {
				t.Fatalf("rerun %d: ok=%v err=%v", i, ok, err)
			}
			continue
		}
		if state != "blocked" || row.state != "blocked" || row.blockedReason.String != IntentBlockedExhaustLimit {
			t.Fatalf("settle %d: state=%q row=%+v, want blocked %s", i, state, row, IntentBlockedExhaustLimit)
		}
	}
}

func TestSettleIntentClaimExhaustedKeepsFence(t *testing.T) {
	_, a, _ := openClaimInstances(t)
	id := addClaimIntent(t, a)
	claimIntentToken(t, a, id, "work#1")
	if _, err := a.SettleIntentClaimExhausted(id, "stale-token"); !errors.Is(err, ErrIntentClaimLost) {
		t.Fatalf("stale token err=%v, want ErrIntentClaimLost", err)
	}
}
