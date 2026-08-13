package engines

import "testing"

func TestCompatibilityForDalleMini(t *testing.T) {
	value, ok := CompatibilityFor("dalle-mini")
	if !ok {
		t.Fatal("dalle-mini compatibility entry is missing")
	}
	if value.Status != "unsupported" || value.Code != "DALL_E_MINI_UNSUPPORTED" {
		t.Fatalf("unexpected compatibility entry: %+v", value)
	}
}

func TestCompatibilityForUnknownEngine(t *testing.T) {
	if _, ok := CompatibilityFor("not-an-engine"); ok {
		t.Fatal("unknown engine unexpectedly has a compatibility entry")
	}
}

func TestCompatibilityListIsStable(t *testing.T) {
	values := CompatibilityList()
	if len(values) != 1 || values[0].ID != "dalle-mini" {
		t.Fatalf("unexpected compatibility list: %+v", values)
	}
	values[0].ID = "mutated"
	value, _ := CompatibilityFor("dalle-mini")
	if value.ID != "dalle-mini" {
		t.Fatal("compatibility catalog was mutated through a returned value")
	}
}
