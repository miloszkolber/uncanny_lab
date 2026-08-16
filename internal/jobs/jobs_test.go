package jobs

import (
	"encoding/json"
	"testing"
)

func TestValidateCreateRejectsInvalidParameters(t *testing.T) {
	for _, parameters := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`broken`)} {
		err := ValidateCreate(CreateRequest{Engine: "test-pattern", Parameters: parameters})
		if err == nil {
			t.Fatalf("ValidateCreate accepted %s", parameters)
		}
	}
}
