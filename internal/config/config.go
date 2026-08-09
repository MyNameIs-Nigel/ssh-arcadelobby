// Package config loads router settings from ARCADE_* environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds server settings loaded from ARCADE_* environment variables.
type Config struct {
	ListenHost          string
	ListenPort          int
	HostKeyPath         string
	ProxyKeyPath        string
	GamesPath           string
	BannerPath          string
	DBPath              string
	LobbyIdleTimeout    time.Duration
	ProbeInterval       time.Duration
	MaxConnections      int
	MaxSessionsPerKey   int
	RateLimitPerSecond  float64
	RateLimitBurst      int
	RateLimitMaxEntries int
	LogLevel            string
	LogFormat           string
}

// Load reads configuration from the environment with documented defaults.
// Rate limiting is stricter than a single game's — the router is the
// enforcement point for the whole fleet.
func Load() (Config, error) {
	var err error
	cfg := Config{
		ListenHost:   envOr("ARCADE_LISTEN_HOST", "0.0.0.0"),
		HostKeyPath:  envOr("ARCADE_HOST_KEY_PATH", "var/ssh_host_key"),
		ProxyKeyPath: envOr("ARCADE_PROXY_KEY_PATH", "var/proxy_key"),
		GamesPath:    envOr("ARCADE_GAMES_PATH", "games.dev.toml"),
		BannerPath:   envOr("ARCADE_BANNER_PATH", "banner.toml"),
		DBPath:       envOr("ARCADE_DB_PATH", "var/arcade.db"),
		LogLevel:     envOr("ARCADE_LOG_LEVEL", "info"),
		LogFormat:    envOr("ARCADE_LOG_FORMAT", "text"),
	}
	if cfg.ListenPort, err = envIntOr("ARCADE_LISTEN_PORT", 22); err != nil {
		return Config{}, err
	}
	if cfg.LobbyIdleTimeout, err = envDurationOr("ARCADE_LOBBY_IDLE_TIMEOUT", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.ProbeInterval, err = envDurationOr("ARCADE_PROBE_INTERVAL", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MaxConnections, err = envIntOr("ARCADE_MAX_CONNECTIONS", 200); err != nil {
		return Config{}, err
	}
	// A player may be in 2 games + 2 lobbies.
	if cfg.MaxSessionsPerKey, err = envIntOr("ARCADE_MAX_SESSIONS_PER_KEY", 4); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitPerSecond, err = envFloatOr("ARCADE_RATE_LIMIT_PER_SECOND", 2); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitBurst, err = envIntOr("ARCADE_RATE_LIMIT_BURST", 5); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitMaxEntries, err = envIntOr("ARCADE_RATE_LIMIT_MAX_IPS", 1000); err != nil {
		return Config{}, err
	}

	if cfg.ListenPort < 1 || cfg.ListenPort > 65535 {
		return Config{}, fmt.Errorf("ARCADE_LISTEN_PORT must be 1-65535, got %d", cfg.ListenPort)
	}
	if cfg.HostKeyPath == "" {
		return Config{}, fmt.Errorf("ARCADE_HOST_KEY_PATH must not be empty")
	}
	if cfg.ProxyKeyPath == "" {
		return Config{}, fmt.Errorf("ARCADE_PROXY_KEY_PATH must not be empty")
	}
	if cfg.GamesPath == "" {
		return Config{}, fmt.Errorf("ARCADE_GAMES_PATH must not be empty")
	}
	if cfg.BannerPath == "" {
		return Config{}, fmt.Errorf("ARCADE_BANNER_PATH must not be empty")
	}
	if cfg.DBPath == "" {
		return Config{}, fmt.Errorf("ARCADE_DB_PATH must not be empty")
	}
	if cfg.LobbyIdleTimeout < 0 {
		return Config{}, fmt.Errorf("ARCADE_LOBBY_IDLE_TIMEOUT must be zero or positive")
	}
	if cfg.ProbeInterval < time.Second {
		return Config{}, fmt.Errorf("ARCADE_PROBE_INTERVAL must be at least 1s")
	}
	if cfg.MaxConnections < 1 {
		return Config{}, fmt.Errorf("ARCADE_MAX_CONNECTIONS must be at least 1")
	}
	if cfg.MaxSessionsPerKey < 1 {
		return Config{}, fmt.Errorf("ARCADE_MAX_SESSIONS_PER_KEY must be at least 1")
	}
	if cfg.RateLimitPerSecond <= 0 {
		return Config{}, fmt.Errorf("ARCADE_RATE_LIMIT_PER_SECOND must be positive")
	}
	if cfg.RateLimitBurst < 1 {
		return Config{}, fmt.Errorf("ARCADE_RATE_LIMIT_BURST must be at least 1")
	}

	return cfg, nil
}

func (c Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.ListenHost, c.ListenPort)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q", key, v)
	}
	return n, nil
}

func envFloatOr(key string, fallback float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid float %q", key, v)
	}
	return f, nil
}

func envDurationOr(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q", key, v)
	}
	return d, nil
}
