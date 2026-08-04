package config

import (
	"os"
	"path/filepath"
	"slices"
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
	if got, want := cfg.AllowedExpiryIDs, []string{"1h", "24h", "72h"}; !slices.Equal(got, want) {
		t.Errorf("AllowedExpiryIDs = %v, want %v", got, want)
	}
	if cfg.LiveRoomLifetime != 24*time.Hour || cfg.LiveMaxTabs != 8 || cfg.LiveMaxBytes != 1<<20 || cfg.LiveMaxParticipants != 32 || cfg.LiveMaxMessageBytes != 64<<10 {
		t.Fatalf("live defaults = lifetime %s, tabs %d, bytes %d, participants %d, message bytes %d", cfg.LiveRoomLifetime, cfg.LiveMaxTabs, cfg.LiveMaxBytes, cfg.LiveMaxParticipants, cfg.LiveMaxMessageBytes)
	}
	if cfg.LiveHeartbeatInterval != 20*time.Second || cfg.LiveMaxConnections != 1000 {
		t.Fatalf("live connection defaults = heartbeat %s, connections %d", cfg.LiveHeartbeatInterval, cfg.LiveMaxConnections)
	}
	if cfg.LiveSnapshotLimits != (LiveSnapshotLimits{MaxRows: 1000, MaxBytes: 4 << 20}) {
		t.Fatalf("live snapshot defaults = %#v", cfg.LiveSnapshotLimits)
	}
	if cfg.LiveCreateRate.Count != 10 || cfg.LiveUnlockRate.Count != 10 || cfg.LiveConnectionRate.Count != 60 || cfg.LiveMessageRate.Count != 600 {
		t.Fatalf("live rate defaults = create %#v unlock %#v connection %#v message %#v", cfg.LiveCreateRate, cfg.LiveUnlockRate, cfg.LiveConnectionRate, cfg.LiveMessageRate)
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
		{"default expiry", "OXBIN_DEFAULT_EXPIRY", "73h"},
		{"allowed expiry beyond 72 hours", "OXBIN_ALLOWED_EXPIRIES", "73h"},
		{"allowed expiry", "OXBIN_ALLOWED_EXPIRIES", "1h,1h"},
		{"rate", "OXBIN_CREATE_RATE", "fifteen/hour"},
		{"consume rate", "OXBIN_CONSUME_RATE", "fifteen/hour"},
		{"trusted proxy", "OXBIN_TRUSTED_PROXIES", "not-a-cidr"},
		{"creation switch", "OXBIN_CREATION_ENABLED", "sometimes"},
		{"timeout", "OXBIN_SHUTDOWN_TIMEOUT", "0s"},
		{"live lifetime", "OXBIN_LIVE_ROOM_LIFETIME", "25h"},
		{"live tabs", "OXBIN_LIVE_MAX_TABS", "65"},
		{"live bytes", "OXBIN_LIVE_MAX_BYTES", "0"},
		{"live participants", "OXBIN_LIVE_MAX_PARTICIPANTS", "257"},
		{"live message bytes", "OXBIN_LIVE_MAX_MESSAGE_BYTES", "1048577"},
		{"live heartbeat", "OXBIN_LIVE_HEARTBEAT_INTERVAL", "1s"},
		{"live create rate", "OXBIN_LIVE_CREATE_RATE", "bad"},
		{"live snapshot limits", "OXBIN_LIVE_SNAPSHOT_LIMITS", "1000"},
		{"live connections", "OXBIN_LIVE_MAX_CONNECTIONS", "10001"},
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

func TestLoadLiveLimits(t *testing.T) {
	t.Parallel()

	cfg, err := Load(func(key string) (string, bool) {
		values := map[string]string{
			"OXBIN_LIVE_ROOM_LIFETIME":      "12h",
			"OXBIN_LIVE_MAX_TABS":           "16",
			"OXBIN_LIVE_MAX_BYTES":          "2097152",
			"OXBIN_LIVE_MAX_PARTICIPANTS":   "64",
			"OXBIN_LIVE_MAX_MESSAGE_BYTES":  "131072",
			"OXBIN_LIVE_HEARTBEAT_INTERVAL": "30s",
			"OXBIN_LIVE_CREATE_RATE":        "20/1h",
			"OXBIN_LIVE_UNLOCK_RATE":        "12/10m",
			"OXBIN_LIVE_CONNECTION_RATE":    "120/1m",
			"OXBIN_LIVE_MESSAGE_RATE":       "1200/1m",
			"OXBIN_LIVE_MAX_CONNECTIONS":    "2000",
			"OXBIN_LIVE_SNAPSHOT_LIMITS":    "2000/8388608",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LiveRoomLifetime != 12*time.Hour || cfg.LiveMaxTabs != 16 || cfg.LiveMaxBytes != 2<<20 || cfg.LiveMaxParticipants != 64 || cfg.LiveMaxMessageBytes != 128<<10 || cfg.LiveHeartbeatInterval != 30*time.Second || cfg.LiveMaxConnections != 2000 {
		t.Fatalf("live limits = %#v", cfg)
	}
	if cfg.LiveSnapshotLimits != (LiveSnapshotLimits{MaxRows: 2000, MaxBytes: 8 << 20}) {
		t.Fatalf("live snapshot limits = %#v", cfg.LiveSnapshotLimits)
	}
}

func TestLoadRejectsIncoherentLiveLimits(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) (string, bool) {
		values := map[string]string{
			"OXBIN_LIVE_MAX_BYTES":         "1024",
			"OXBIN_LIVE_MAX_MESSAGE_BYTES": "2048",
		}
		value, ok := values[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "OXBIN_LIVE_MAX_MESSAGE_BYTES") {
		t.Fatalf("Load() error = %v, want max-message incoherence", err)
	}

	_, err = Load(func(key string) (string, bool) {
		values := map[string]string{
			"OXBIN_LIVE_MAX_BYTES":       "2097152",
			"OXBIN_LIVE_SNAPSHOT_LIMITS": "1000/1048576",
		}
		value, ok := values[key]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), "OXBIN_LIVE_SNAPSHOT_LIMITS") {
		t.Fatalf("Load() error = %v, want snapshot incoherence", err)
	}
}

func TestLoadAccepts72HourExpiry(t *testing.T) {
	t.Parallel()

	cfg, err := Load(func(key string) (string, bool) {
		if key == "OXBIN_ALLOWED_EXPIRIES" {
			return "72h", true
		}
		if key == "OXBIN_DEFAULT_EXPIRY" {
			return "72h", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DefaultExpiry != 72*time.Hour {
		t.Errorf("DefaultExpiry = %s, want 72h", cfg.DefaultExpiry)
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
