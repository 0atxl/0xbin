package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLiveRoomLifetime    = 24 * time.Hour
	defaultLiveMaxTabs         = 8
	defaultLiveMaxBytes        = int64(1 << 20)
	defaultLiveMaxWriters      = 10
	defaultLiveMaxViewers      = 100
	defaultLiveMaxParticipants = defaultLiveMaxWriters + defaultLiveMaxViewers
	defaultLiveMaxMessageBytes = 64 << 10
	defaultLiveHeartbeat       = 20 * time.Second
	defaultLiveReconnectGrace  = 30 * time.Second
	defaultLiveParticipantTTL  = 60 * time.Second
	defaultLiveMaxConnections  = 1000
	defaultLiveSnapshotRows    = 1000
	defaultLiveSnapshotBytes   = int64(4 << 20)
	maxLiveRoomLifetime        = 24 * time.Hour
	maxLiveTabs                = 64
	maxLiveBytes               = int64(8 << 20)
	maxLiveParticipants        = 256
	maxLiveMessageBytes        = 1 << 20
	maxLiveConnections         = 10000
	maxLiveSnapshotRows        = 100000
	maxLiveSnapshotBytes       = int64(64 << 20)
)

type liveConfig struct {
	roomLifetime       time.Duration
	maxTabs            int
	maxBytes           int64
	maxWriters         int
	maxViewers         int
	maxParticipants    int
	maxMessageBytes    int
	heartbeatInterval  time.Duration
	reconnectGrace     time.Duration
	participantTimeout time.Duration
	createRate         Rate
	unlockRate         Rate
	connectionRate     Rate
	messageRate        Rate
	maxConnections     int
	snapshotLimits     LiveSnapshotLimits
}

func loadLiveConfig(get func(string, string) string) (liveConfig, error) {
	roomLifetime, err := parseBoundedDuration("OXBIN_LIVE_ROOM_LIFETIME", get("OXBIN_LIVE_ROOM_LIFETIME", defaultLiveRoomLifetime.String()), time.Minute, maxLiveRoomLifetime)
	if err != nil {
		return liveConfig{}, err
	}
	maxTabs, err := parseBoundedInt("OXBIN_LIVE_MAX_TABS", get("OXBIN_LIVE_MAX_TABS", strconv.Itoa(defaultLiveMaxTabs)), 1, maxLiveTabs)
	if err != nil {
		return liveConfig{}, err
	}
	maxBytes, err := parseBoundedInt64("OXBIN_LIVE_MAX_BYTES", get("OXBIN_LIVE_MAX_BYTES", strconv.FormatInt(defaultLiveMaxBytes, 10)), 1, maxLiveBytes)
	if err != nil {
		return liveConfig{}, err
	}
	maxWriters, err := parseBoundedInt("OXBIN_LIVE_MAX_WRITERS", get("OXBIN_LIVE_MAX_WRITERS", strconv.Itoa(defaultLiveMaxWriters)), 1, maxLiveParticipants)
	if err != nil {
		return liveConfig{}, err
	}
	maxViewers, err := parseBoundedInt("OXBIN_LIVE_MAX_VIEWERS", get("OXBIN_LIVE_MAX_VIEWERS", strconv.Itoa(defaultLiveMaxViewers)), 0, maxLiveParticipants)
	if err != nil {
		return liveConfig{}, err
	}
	maxParticipants, err := parseBoundedInt("OXBIN_LIVE_MAX_PARTICIPANTS", get("OXBIN_LIVE_MAX_PARTICIPANTS", strconv.Itoa(defaultLiveMaxParticipants)), 1, maxLiveParticipants)
	if err != nil {
		return liveConfig{}, err
	}
	maxMessageBytes, err := parseBoundedInt("OXBIN_LIVE_MAX_MESSAGE_BYTES", get("OXBIN_LIVE_MAX_MESSAGE_BYTES", strconv.Itoa(defaultLiveMaxMessageBytes)), 1, maxLiveMessageBytes)
	if err != nil {
		return liveConfig{}, err
	}
	heartbeatInterval, err := parseBoundedDuration("OXBIN_LIVE_HEARTBEAT_INTERVAL", get("OXBIN_LIVE_HEARTBEAT_INTERVAL", defaultLiveHeartbeat.String()), 5*time.Second, 5*time.Minute)
	if err != nil {
		return liveConfig{}, err
	}
	reconnectGrace, err := parseBoundedDuration("OXBIN_LIVE_RECONNECT_GRACE", get("OXBIN_LIVE_RECONNECT_GRACE", defaultLiveReconnectGrace.String()), 5*time.Second, 5*time.Minute)
	if err != nil {
		return liveConfig{}, err
	}
	participantTimeout, err := parseBoundedDuration("OXBIN_LIVE_PARTICIPANT_TIMEOUT", get("OXBIN_LIVE_PARTICIPANT_TIMEOUT", defaultLiveParticipantTTL.String()), 10*time.Second, 10*time.Minute)
	if err != nil {
		return liveConfig{}, err
	}
	createRate, err := parseRate("OXBIN_LIVE_CREATE_RATE", get("OXBIN_LIVE_CREATE_RATE", "10/1h"))
	if err != nil {
		return liveConfig{}, err
	}
	unlockRate, err := parseRate("OXBIN_LIVE_UNLOCK_RATE", get("OXBIN_LIVE_UNLOCK_RATE", "10/15m"))
	if err != nil {
		return liveConfig{}, err
	}
	connectionRate, err := parseRate("OXBIN_LIVE_CONNECTION_RATE", get("OXBIN_LIVE_CONNECTION_RATE", "60/1m"))
	if err != nil {
		return liveConfig{}, err
	}
	messageRate, err := parseRate("OXBIN_LIVE_MESSAGE_RATE", get("OXBIN_LIVE_MESSAGE_RATE", "2400/1m"))
	if err != nil {
		return liveConfig{}, err
	}
	maxConnections, err := parseBoundedInt("OXBIN_LIVE_MAX_CONNECTIONS", get("OXBIN_LIVE_MAX_CONNECTIONS", strconv.Itoa(defaultLiveMaxConnections)), 1, maxLiveConnections)
	if err != nil {
		return liveConfig{}, err
	}
	snapshotLimits, err := parseSnapshotLimits(get("OXBIN_LIVE_SNAPSHOT_LIMITS", strconv.Itoa(defaultLiveSnapshotRows)+"/"+strconv.FormatInt(defaultLiveSnapshotBytes, 10)))
	if err != nil {
		return liveConfig{}, err
	}
	if int64(maxMessageBytes) > maxBytes {
		return liveConfig{}, fmt.Errorf("OXBIN_LIVE_MAX_MESSAGE_BYTES must not exceed OXBIN_LIVE_MAX_BYTES")
	}
	if snapshotLimits.MaxBytes < maxBytes {
		return liveConfig{}, fmt.Errorf("OXBIN_LIVE_SNAPSHOT_LIMITS bytes must be at least OXBIN_LIVE_MAX_BYTES")
	}
	if maxWriters+maxViewers != maxParticipants {
		return liveConfig{}, fmt.Errorf("OXBIN_LIVE_MAX_PARTICIPANTS must equal OXBIN_LIVE_MAX_WRITERS plus OXBIN_LIVE_MAX_VIEWERS")
	}
	if participantTimeout < 2*heartbeatInterval {
		return liveConfig{}, fmt.Errorf("OXBIN_LIVE_PARTICIPANT_TIMEOUT must be at least twice OXBIN_LIVE_HEARTBEAT_INTERVAL")
	}
	if reconnectGrace >= participantTimeout {
		return liveConfig{}, fmt.Errorf("OXBIN_LIVE_RECONNECT_GRACE must be shorter than OXBIN_LIVE_PARTICIPANT_TIMEOUT")
	}
	return liveConfig{
		roomLifetime:       roomLifetime,
		maxTabs:            maxTabs,
		maxBytes:           maxBytes,
		maxWriters:         maxWriters,
		maxViewers:         maxViewers,
		maxParticipants:    maxParticipants,
		maxMessageBytes:    maxMessageBytes,
		heartbeatInterval:  heartbeatInterval,
		reconnectGrace:     reconnectGrace,
		participantTimeout: participantTimeout,
		createRate:         createRate,
		unlockRate:         unlockRate,
		connectionRate:     connectionRate,
		messageRate:        messageRate,
		maxConnections:     maxConnections,
		snapshotLimits:     snapshotLimits,
	}, nil
}

func parseBoundedDuration(name, value string, minimum, maximum time.Duration) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration < minimum || duration > maximum {
		return 0, fmt.Errorf("%s must be between %s and %s", name, minimum, maximum)
	}
	return duration, nil
}

func parseBoundedInt(name, value string, minimum, maximum int) (int, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return int(parsed), nil
}

func parseBoundedInt64(name, value string, minimum, maximum int64) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func parseSnapshotLimits(value string) (LiveSnapshotLimits, error) {
	rowsText, bytesText, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(bytesText, "/") {
		return LiveSnapshotLimits{}, fmt.Errorf("OXBIN_LIVE_SNAPSHOT_LIMITS must use rows/bytes syntax")
	}
	rows, err := parseBoundedInt("OXBIN_LIVE_SNAPSHOT_LIMITS rows", rowsText, 1, maxLiveSnapshotRows)
	if err != nil {
		return LiveSnapshotLimits{}, err
	}
	bytes, err := parseBoundedInt64("OXBIN_LIVE_SNAPSHOT_LIMITS bytes", bytesText, 1, maxLiveSnapshotBytes)
	if err != nil {
		return LiveSnapshotLimits{}, err
	}
	return LiveSnapshotLimits{MaxRows: rows, MaxBytes: bytes}, nil
}
