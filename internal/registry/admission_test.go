package registry

import "testing"

func TestGuestPolicyParsedAndDefaultDenied(t *testing.T) {
	games, err := validate([]fileGame{{ID: "farm", Name: "Farm", Addr: "127.0.0.1:22", AllowGuests: true}, {ID: "chess", Name: "Chess", Addr: "127.0.0.1:23"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range games {
		if g.AllowGuests != (g.ID == "farm") {
			t.Fatal("allow_guests not propagated")
		}
	}
	for _, g := range games {
		_, err := Playable([]GameStatus{{Game: g, Online: true}}, g.ID, true)
		if (err == nil) != (g.ID == "farm") {
			t.Fatal("guest admission did not fail closed")
		}
		if _, err := Playable([]GameStatus{{Game: g, Online: true}}, g.ID, false); err != nil {
			t.Fatal("keyed admission changed")
		}
	}
}
