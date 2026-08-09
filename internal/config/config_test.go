package config

import (
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenPort != 22 || cfg.ListenHost != "0.0.0.0" {
		t.Fatalf("unexpected listen defaults: %+v", cfg)
	}
	if cfg.HostKeyPath != "var/ssh_host_key" || cfg.ProxyKeyPath != "var/proxy_key" {
		t.Fatalf("unexpected key defaults: %+v", cfg)
	}
	if cfg.GamesPath != "games.dev.toml" {
		t.Fatalf("unexpected games path: %q", cfg.GamesPath)
	}
	if cfg.BannerPath != "banner.toml" {
		t.Fatalf("unexpected banner path: %q", cfg.BannerPath)
	}
	if cfg.DBPath != "var/arcade.db" {
		t.Fatalf("unexpected db path: %q", cfg.DBPath)
	}
	if cfg.LobbyIdleTimeout != 5*time.Minute || cfg.ProbeInterval != 15*time.Second {
		t.Fatalf("unexpected duration defaults: %+v", cfg)
	}
	if cfg.MaxConnections != 200 || cfg.MaxSessionsPerKey != 4 {
		t.Fatalf("unexpected caps: %+v", cfg)
	}
	if cfg.RateLimitPerSecond != 2 || cfg.RateLimitBurst != 5 {
		t.Fatalf("unexpected rate limits: %+v", cfg)
	}
	if cfg.ListenAddr() != "0.0.0.0:22" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr())
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string]string{
		"ARCADE_LISTEN_PORT":          "70000",
		"ARCADE_MAX_CONNECTIONS":      "0",
		"ARCADE_MAX_SESSIONS_PER_KEY": "0",
		"ARCADE_RATE_LIMIT_PER_SECOND": "-1",
		"ARCADE_RATE_LIMIT_BURST":     "0",
		"ARCADE_PROBE_INTERVAL":       "10ms",
		"ARCADE_LOBBY_IDLE_TIMEOUT":   "-5m",
	}
	for key, val := range cases {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, val)
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%s", key, val)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Setenv("ARCADE_LISTEN_PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a non-integer port")
	}
}
