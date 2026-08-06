package jobs

import (
	"encoding/json"
	"testing"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name string
		from Status
		to   Status
		want bool
	}{
		{name: "queued starts preparing", from: Queued, to: Preparing, want: true},
		{name: "running completes", from: Running, to: Completed, want: true},
		{name: "completed cannot restart", from: Completed, to: Running, want: false},
		{name: "queued cannot complete directly", from: Queued, to: Completed, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CanTransition(test.from, test.to); got != test.want {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
			}
		})
	}
}

func TestValidateCreateRejectsInvalidParameters(t *testing.T) {
	for _, parameters := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`broken`)} {
		err := ValidateCreate(CreateRequest{Engine: "test-pattern", Parameters: parameters})
		if err == nil {
			t.Fatalf("ValidateCreate accepted %s", parameters)
		}
	}
}
