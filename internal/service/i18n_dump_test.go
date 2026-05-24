package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestDumpI18n writes translations[de] and translations[en] to flat JSON
// files at $I18N_OUT/de.json and $I18N_OUT/en.json. It's a no-op unless
// I18N_OUT is set, so it doesn't run in normal CI.
//
//	I18N_OUT=$(pwd)/web/src/i18n go test -run TestDumpI18n ./internal/service/
func TestDumpI18n(t *testing.T) {
	out := os.Getenv("I18N_OUT")
	if out == "" {
		t.Skip("set I18N_OUT=<dir> to dump web/src/i18n/{de,en}.json")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", out, err)
	}
	for lang, m := range translations {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]string, len(m))
		// json.Marshal on map sorts keys alphabetically; that gives us a
		// stable diff without us building an ordered struct.
		for _, k := range keys {
			ordered[k] = m[k]
		}
		b, err := json.MarshalIndent(ordered, "", "  ")
		if err != nil {
			t.Fatalf("marshal %s: %v", lang, err)
		}
		b = append(b, '\n')
		path := filepath.Join(out, string(lang)+".json")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d keys)", path, len(m))
	}
}
