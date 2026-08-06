package engines

import (
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
	if models[0].Status != "available" || models[0].Hash == "" {
		t.Fatalf("available model missing hash: %+v", models[0])
	}
	if models[1].Status != "invalid" {
		t.Fatalf("escaped model status = %s", models[1].Status)
	}
}
