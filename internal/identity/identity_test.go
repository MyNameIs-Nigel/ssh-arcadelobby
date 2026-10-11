package identity

import "testing"

func TestIdentityDisjointAndRandom(t *testing.T) {
	a, b := NewGuest(), NewGuest()
	if a.Validate() != nil || a.GuestID == b.GuestID {
		t.Fatal("invalid guest randomness")
	}
	for _, id := range []Identity{{}, {Kind: Guest}, {Kind: Guest, GuestID: a.GuestID, Fingerprint: "SHA256:x"}, {Kind: Guest, GuestID: a.GuestID, Slot: "guest"}, {Kind: Guest, GuestID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, {Kind: Keyed, GuestID: a.GuestID}} {
		if id.Validate() == nil {
			t.Fatalf("accepted %+v", id)
		}
	}
}
