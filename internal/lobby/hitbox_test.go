package lobby

import "testing"

func TestHitboxAt(t *testing.T) {
	h := &Hitboxes{}
	h.Add(Box{X: 1, Y: 2, W: 10, H: 2, ID: "a"})
	h.Add(Box{X: 5, Y: 3, W: 4, H: 1, ID: "b"}) // overlaps a's second row

	cases := []struct {
		x, y   int
		wantID string
		hit    bool
	}{
		{1, 2, "a", true},
		{10, 3, "a", true},
		{0, 2, "", false},   // left of a
		{11, 2, "", false},  // right edge exclusive
		{1, 4, "", false},   // below a
		{5, 3, "b", true},   // overlap: topmost (last-registered) wins
		{8, 3, "b", true},
		{9, 3, "a", true},   // past b's right edge, still inside a
		{5, 2, "a", true},   // b only covers row 3
	}
	for _, tc := range cases {
		box, ok := h.At(tc.x, tc.y)
		if ok != tc.hit {
			t.Errorf("At(%d,%d) hit = %v, want %v", tc.x, tc.y, ok, tc.hit)
			continue
		}
		if ok && box.ID != tc.wantID {
			t.Errorf("At(%d,%d) = %q, want %q", tc.x, tc.y, box.ID, tc.wantID)
		}
	}
}

func TestHitboxResetClearsOldBoxes(t *testing.T) {
	h := &Hitboxes{}
	h.Add(Box{X: 0, Y: 0, W: 5, H: 1, ID: "stale"})
	h.Reset()
	if _, ok := h.At(0, 0); ok {
		t.Fatal("box survived Reset")
	}
	h.Add(Box{X: 0, Y: 0, W: 5, H: 1, ID: "fresh"})
	if box, ok := h.At(0, 0); !ok || box.ID != "fresh" {
		t.Fatalf("expected fresh box, got %+v ok=%v", box, ok)
	}
}
