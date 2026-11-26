package server

import "testing"

func TestFindingStatusFilter(t *testing.T) {
	cases := map[string]string{
		"":                                   "",
		"all":                                "",
		"pending":                            "pending",
		"pending,confirmed":                  "pending,confirmed",
		" pending , fixed ,pending":          "pending,fixed",
		"pending,bogus,risk_accepted":        "pending,risk_accepted",
		"bogus,' OR 1=1 --":                  "",
		"false_positive,ignored,duplicate,x": "false_positive,ignored,duplicate",
	}
	for in, want := range cases {
		if got := findingStatusFilter(in); got != want {
			t.Errorf("findingStatusFilter(%q) = %q, want %q", in, got, want)
		}
	}
}
