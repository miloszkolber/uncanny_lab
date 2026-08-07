package engines

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestModelPathContainmentAndHash(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "registry"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "model.bin"), []byte("model"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "registry", "ok.json"), []byte(`{"id":"model-a","path":"model.bin"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "registry", "bad.json"), []byte(`{"id":"model-b","path":"../outside.bin"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	models, err := LoadModels(root, &Registry{})
	if err != nil {
		t.Fatal(err)
	}
	if models[0].Status != "available" || models[0].Hash != "" {
		t.Fatalf("model listing should avoid eager hashing: %+v", models[0])
	}
	verified, err := VerifyModel(root, "model-a", &Registry{})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != "available" || verified.Hash == "" {
		t.Fatalf("verified model missing hash: %+v", verified)
	}
	if models[1].Status != "invalid" {
		t.Fatalf("escaped model status = %s", models[1].Status)
	}
}

func TestModelListingEnforcesConfiguredHash(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "registry"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "model.bin"), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	descriptor := `{"id":"model-a","path":"model.bin","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`
	if err := os.WriteFile(filepath.Join(root, "registry", "model.json"), []byte(descriptor), 0o640); err != nil {
		t.Fatal(err)
	}
	models, err := LoadModels(root, &Registry{})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Status != "invalid" || models[0].Hash == "" {
		t.Fatalf("configured hash was not enforced: %+v", models)
	}
}

func TestManifestApplyDefaultsPreservesExplicitValues(t *testing.T) {
	manifest := Manifest{Parameters: map[string]interface{}{
		"iterations": map[string]interface{}{"type": "integer", "default": 250},
		"prompt":     map[string]interface{}{"type": "string", "default": "default prompt"},
	}}
	normalized, err := manifest.ApplyDefaults(json.RawMessage(`{"iterations":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]interface{}
	if err := json.Unmarshal(normalized, &values); err != nil {
		t.Fatal(err)
	}
	if values["iterations"] != float64(1) || values["prompt"] != "default prompt" {
		t.Fatalf("normalized parameters = %#v", values)
	}
}
