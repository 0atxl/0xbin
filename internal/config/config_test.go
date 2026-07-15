package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.BaseURL.String(), "http://localhost:8080"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}
	if got, want := cfg.ListenAddr, "127.0.0.1:8080"; got != want {
		t.Errorf("ListenAddr = %q, want %q", got, want)
	}
	if cfg.DefaultExpiry != 24*time.Hour {
		t.Errorf("DefaultExpiry = %s, want 24h", cfg.DefaultExpiry)
	}
}

func TestLoadRejectsDataFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(func(key string) (string, bool) {
		if key == "OXBIN_DATA_DIR" {
			return path, true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "OXBIN_DATA_DIR") {
		t.Fatalf("Load() error = %v, want data-directory error", err)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"base URL", "OXBIN_BASE_URL", "ftp://example.com"},
		{"listen address", "OXBIN_LISTEN_ADDR", "not-an-address"},
		{"data path", "OXBIN_DATA_DIR", "\x00"},
		{"paste limit", "OXBIN_MAX_PASTE_BYTES", "1048577"},
		{"default expiry", "OXBIN_DEFAULT_EXPIRY", "25h"},
		{"allowed expiry", "OXBIN_ALLOWED_EXPIRIES", "1h,1h"},
		{"rate", "OXBIN_CREATE_RATE", "fifteen/hour"},
		{"trusted proxy", "OXBIN_TRUSTED_PROXIES", "not-a-cidr"},
		{"creation switch", "OXBIN_CREATION_ENABLED", "sometimes"},
		{"timeout", "OXBIN_SHUTDOWN_TIMEOUT", "0s"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(func(key string) (string, bool) {
				if key == test.key {
					return test.value, true
				}
				return "", false
			})
			if err == nil {
				t.Fatal("Load() error = nil")
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Errorf("Load() error = %q, want it to mention %s", err, test.key)
			}
		})
	}
}

func TestLoadRequiresDefaultExpiryToBeAllowed(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) (string, bool) {
		switch key {
		case "OXBIN_ALLOWED_EXPIRIES":
			return "1h", true
		case "OXBIN_DEFAULT_EXPIRY":
			return "24h", true
		default:
			return "", false
		}
	})
	if err == nil || !strings.Contains(err.Error(), "OXBIN_DEFAULT_EXPIRY") {
		t.Fatalf("Load() error = %v, want default-expiry error", err)
	}
}
