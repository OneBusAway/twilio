package localization

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// referenceLocale is the source of truth for the set of localization keys.
// Every other locale file must define exactly the same keys so that no user
// ever silently falls back to English for a missing string.
const referenceLocale = "en-US.json"

// localesDirForParity points at the repository's real locale files (one level
// up from this package), so the test guards the shipped translations rather
// than a fixture.
const localesDirForParity = "../locales"

func loadLocaleKeys(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	var strings map[string]string
	if err := json.Unmarshal(data, &strings); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	keys := make(map[string]struct{}, len(strings))
	for k := range strings {
		keys[k] = struct{}{}
	}
	return keys
}

// TestLocaleKeyParity ensures every locale file defines the same set of keys as
// the reference (en-US) locale. Missing keys cause the English fallback to leak
// into other languages; extra keys are dead translations.
func TestLocaleKeyParity(t *testing.T) {
	refKeys := loadLocaleKeys(t, filepath.Join(localesDirForParity, referenceLocale))

	entries, err := os.ReadDir(localesDirForParity)
	if err != nil {
		t.Fatalf("failed to read locales dir: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || entry.Name() == referenceLocale {
			continue
		}

		localeKeys := loadLocaleKeys(t, filepath.Join(localesDirForParity, entry.Name()))

		var missing, extra []string
		for k := range refKeys {
			if _, ok := localeKeys[k]; !ok {
				missing = append(missing, k)
			}
		}
		for k := range localeKeys {
			if _, ok := refKeys[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)

		if len(missing) > 0 {
			t.Errorf("%s is missing keys present in %s: %v", entry.Name(), referenceLocale, missing)
		}
		if len(extra) > 0 {
			t.Errorf("%s has keys not present in %s: %v", entry.Name(), referenceLocale, extra)
		}
	}
}

// TestLocaleFilesEndWithNewline ensures every locale file ends with a trailing
// newline for consistency and POSIX-friendly diffs.
func TestLocaleFilesEndWithNewline(t *testing.T) {
	entries, err := os.ReadDir(localesDirForParity)
	if err != nil {
		t.Fatalf("failed to read locales dir: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(localesDirForParity, entry.Name()))
		if err != nil {
			t.Fatalf("failed to read %s: %v", entry.Name(), err)
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Errorf("%s does not end with a trailing newline", entry.Name())
		}
	}
}
