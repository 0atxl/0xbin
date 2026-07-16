// Package config loads and validates process configuration.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxPasteBytes = 1 << 20

// LookupEnv is compatible with os.LookupEnv and makes configuration testable.
type LookupEnv func(string) (string, bool)

// Rate describes one rate-limit bucket. Enforcement is added in Step 7.
type Rate struct {
	Count  int
	Window time.Duration
}

// Config is the validated runtime configuration for one 0xbin instance.
type Config struct {
	BaseURL           *url.URL
	ListenAddr        string
	DataDir           string
	MaxPasteBytes     int64
	DefaultExpiry     time.Duration
	AllowedExpiries   []time.Duration
	AllowedExpiryIDs  []string
	CreateRate        Rate
	ReadRate          Rate
	MissRate          Rate
	ConsumeRate       Rate
	TrustedProxies    []netip.Prefix
	CreationEnabled   bool
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// Load creates a Config from OXBIN_* environment variables.
func Load(lookup LookupEnv) (Config, error) {
	get := func(key, fallback string) string {
		if value, ok := lookup(key); ok {
			return strings.TrimSpace(value)
		}
		return fallback
	}

	baseURL, err := parseBaseURL(get("OXBIN_BASE_URL", "http://localhost:8080"))
	if err != nil {
		return Config{}, err
	}
	listenAddr, err := parseListenAddr(get("OXBIN_LISTEN_ADDR", "127.0.0.1:8080"))
	if err != nil {
		return Config{}, err
	}
	dataDir, err := parseDataDir(get("OXBIN_DATA_DIR", "./data"))
	if err != nil {
		return Config{}, err
	}
	maxPasteBytes, err := parseMaxPasteBytes(get("OXBIN_MAX_PASTE_BYTES", strconv.Itoa(maxPasteBytes)))
	if err != nil {
		return Config{}, err
	}
	allowedExpiries, err := parseExpiries(get("OXBIN_ALLOWED_EXPIRIES", "1h,24h"))
	if err != nil {
		return Config{}, err
	}
	defaultExpiry, err := parseExpiry(get("OXBIN_DEFAULT_EXPIRY", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("OXBIN_DEFAULT_EXPIRY: %w", err)
	}
	if !containsDuration(allowedExpiries, defaultExpiry) {
		return Config{}, fmt.Errorf("OXBIN_DEFAULT_EXPIRY must be listed in OXBIN_ALLOWED_EXPIRIES")
	}
	createRate, err := parseRate("OXBIN_CREATE_RATE", get("OXBIN_CREATE_RATE", "15/1h"))
	if err != nil {
		return Config{}, err
	}
	readRate, err := parseRate("OXBIN_READ_RATE", get("OXBIN_READ_RATE", "120/1h"))
	if err != nil {
		return Config{}, err
	}
	missRate, err := parseRate("OXBIN_MISS_RATE", get("OXBIN_MISS_RATE", "30/1h"))
	if err != nil {
		return Config{}, err
	}
	consumeRate, err := parseRate("OXBIN_CONSUME_RATE", get("OXBIN_CONSUME_RATE", "30/1h"))
	if err != nil {
		return Config{}, err
	}
	trustedProxies, err := parseTrustedProxies(get("OXBIN_TRUSTED_PROXIES", ""))
	if err != nil {
		return Config{}, err
	}
	creationEnabled, err := strconv.ParseBool(get("OXBIN_CREATION_ENABLED", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("OXBIN_CREATION_ENABLED must be true or false: %w", err)
	}

	readHeaderTimeout, err := parsePositiveDuration("OXBIN_READ_HEADER_TIMEOUT", get("OXBIN_READ_HEADER_TIMEOUT", "5s"))
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := parsePositiveDuration("OXBIN_READ_TIMEOUT", get("OXBIN_READ_TIMEOUT", "15s"))
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := parsePositiveDuration("OXBIN_WRITE_TIMEOUT", get("OXBIN_WRITE_TIMEOUT", "30s"))
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := parsePositiveDuration("OXBIN_IDLE_TIMEOUT", get("OXBIN_IDLE_TIMEOUT", "60s"))
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := parsePositiveDuration("OXBIN_SHUTDOWN_TIMEOUT", get("OXBIN_SHUTDOWN_TIMEOUT", "10s"))
	if err != nil {
		return Config{}, err
	}

	return Config{
		BaseURL:           baseURL,
		ListenAddr:        listenAddr,
		DataDir:           dataDir,
		MaxPasteBytes:     maxPasteBytes,
		DefaultExpiry:     defaultExpiry,
		AllowedExpiries:   allowedExpiries,
		AllowedExpiryIDs:  expiryIDs(get("OXBIN_ALLOWED_EXPIRIES", "1h,24h")),
		CreateRate:        createRate,
		ReadRate:          readRate,
		MissRate:          missRate,
		ConsumeRate:       consumeRate,
		TrustedProxies:    trustedProxies,
		CreationEnabled:   creationEnabled,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ShutdownTimeout:   shutdownTimeout,
	}, nil
}

func expiryIDs(value string) []string {
	parts := strings.Split(value, ",")
	identifiers := make([]string, 0, len(parts))
	for _, part := range parts {
		identifiers = append(identifiers, strings.TrimSpace(part))
	}
	return identifiers
}

func parseBaseURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("OXBIN_BASE_URL must be an absolute http or https URL without credentials, query, or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("OXBIN_BASE_URL must not include a path")
	}
	u.Path = ""
	return u, nil
}

func parseListenAddr(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("OXBIN_LISTEN_ADDR must not be empty")
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		return "", fmt.Errorf("OXBIN_LISTEN_ADDR must be a host:port: %w", err)
	}
	return value, nil
}

func parseDataDir(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("OXBIN_DATA_DIR must not be empty")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("OXBIN_DATA_DIR contains an invalid NUL byte")
	}
	path := filepath.Clean(value)
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return "", fmt.Errorf("OXBIN_DATA_DIR must be a directory")
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("OXBIN_DATA_DIR cannot be inspected: %w", err)
	}
	return path, nil
}

func parseMaxPasteBytes(value string) (int64, error) {
	bytes, err := strconv.ParseInt(value, 10, 64)
	if err != nil || bytes < 1 || bytes > maxPasteBytes {
		return 0, fmt.Errorf("OXBIN_MAX_PASTE_BYTES must be between 1 and %d", maxPasteBytes)
	}
	return bytes, nil
}

func parseExpiries(value string) ([]time.Duration, error) {
	if value == "" {
		return nil, fmt.Errorf("OXBIN_ALLOWED_EXPIRIES must include at least one duration")
	}
	parts := strings.Split(value, ",")
	durations := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		duration, err := parseExpiry(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("OXBIN_ALLOWED_EXPIRIES: %w", err)
		}
		if containsDuration(durations, duration) {
			return nil, fmt.Errorf("OXBIN_ALLOWED_EXPIRIES contains duplicate duration %q", part)
		}
		durations = append(durations, duration)
	}
	return durations, nil
}

func parseExpiry(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration > 24*time.Hour {
		return 0, fmt.Errorf("must be a positive duration no longer than 24h")
	}
	return duration, nil
}

func parseRate(name, value string) (Rate, error) {
	countText, durationText, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(durationText, "/") {
		return Rate{}, fmt.Errorf("%s must use count/duration syntax", name)
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 1 {
		return Rate{}, fmt.Errorf("%s must have a positive count", name)
	}
	window, err := parsePositiveDuration(name, durationText)
	if err != nil {
		return Rate{}, err
	}
	return Rate{Count: count, Window: window}, nil
}

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("OXBIN_TRUSTED_PROXIES contains invalid CIDR %q", part)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}

func containsDuration(durations []time.Duration, target time.Duration) bool {
	for _, duration := range durations {
		if duration == target {
			return true
		}
	}
	return false
}
