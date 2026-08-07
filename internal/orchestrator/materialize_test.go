package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/miloszkolber/uncanny-lab/internal/config"
)

func TestMaterializeImageParametersRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Paths: config.PathsConfig{Inputs: filepath.Join(root, "inputs"), Workspace: filepath.Join(root, "workspace")}}
	if err := os.MkdirAll(cfg.Paths.Inputs, 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.png")
	if err := os.WriteFile(outside, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cfg.Paths.Inputs, "0123456789abcdef0123456789abcdef.png")); err != nil {
		t.Fatal(err)
	}
	_, err := MaterializeImageParameters(cfg, filepath.Join(root, "job"), json.RawMessage(`{"source_image":"inputs/0123456789abcdef0123456789abcdef.png"}`))
	if err == nil {
		t.Fatal("accepted symlink escaping inputs")
	}
}
