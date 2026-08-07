package modelinstall

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCatalogIsFixedAndComplete(t *testing.T) {
	if len(Sources) != 6 || len(Repos) != 2 || len(Outputs) != 5 {
		t.Fatalf("catalog dimensions: sources=%d repos=%d outputs=%d", len(Sources), len(Repos), len(Outputs))
	}
	for _, source := range Sources {
		if !strings.HasPrefix(source.URL, "https://") || source.SHA256 == "" || source.Bytes < 1 {
			t.Fatalf("unsafe catalog source %#v", source)
		}
	}
	for _, repo := range Repos {
		if !strings.HasPrefix(repo.URL, "https://") || len(repo.Commit) != 40 || len(repo.Tree) != 40 {
			t.Fatalf("unsafe repo pin %#v", repo)
		}
	}
}

func TestConverterCatalogDrift(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "tools", "convert_bundle_b.py"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, source := range Sources {
		// Source names are the converter's sole source-hash keys. This catches a
		// release where Go approves a different byte sequence than Python accepts.
		if !regexp.MustCompile(`"` + regexp.QuoteMeta(source.Name) + `": "` + source.SHA256 + `"`).MatchString(text) {
			t.Fatalf("converter source hash drift for %s", source.Name)
		}
	}
	for _, repo := range Repos {
		if !strings.Contains(text, repo.Commit) || !strings.Contains(text, repo.Tree) {
			t.Fatalf("converter repository pin drift for %s", repo.ID)
		}
	}
}
