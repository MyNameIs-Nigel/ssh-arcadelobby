package config

import "testing"

func TestGuestDefaultsAndInvalidSettings(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AllowGuests || c.MaxGuestSessions != 32 || c.MaxGuestSessionsPerIP != 4 || c.MaxPendingAuth != 64 {
		t.Fatalf("bad guest defaults: %+v", c)
	}
	for k, v := range map[string]string{"ARCADE_ALLOW_GUESTS": "maybe", "ARCADE_MAX_GUEST_SESSIONS": "0", "ARCADE_MAX_GUEST_SESSIONS_PER_IP": "0", "ARCADE_MAX_PENDING_AUTH": "0", "ARCADE_AUTH_TIMEOUT": "0s"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := Load(); err == nil {
				t.Fatal("accepted invalid guest setting")
			}
		})
	}
	t.Setenv("ARCADE_ALLOW_GUESTS", "true")
	t.Setenv("ARCADE_MAX_CONNECTIONS", "32")
	if _, err := Load(); err == nil {
		t.Fatal("guest budget must reserve keyed capacity")
	}
}
