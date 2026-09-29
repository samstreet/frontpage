package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feeds.yaml")
	t.Setenv("APP_CONFIG_FILE", path)
	for _, tc := range []struct {
		name, yaml, wantError string
		count                 int
	}{
		{"legacy config", "feeds: []\ninterests: []\n", "", 0},
		{"events", "events:\n  - name: Alex\n    date: \"1990-05-14\"\n    type: birthday\n  - name: Party\n    date: \"2027-07-10\"\n    repeat: once\n", "", 2},
		{"invalid date", "events:\n  - name: Alex\n    date: \"02-30\"\n", "events[0] (Alex): date must be a valid", 0},
		{"missing year", "events:\n  - name: Party\n    date: \"07-10\"\n    repeat: once\n", "events[0] (Party): repeat: once requires", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Load error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Source.Events) != tc.count {
				t.Fatalf("events = %+v", cfg.Source.Events)
			}
		})
	}
}
