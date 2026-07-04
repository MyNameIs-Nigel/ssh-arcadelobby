package lobby

// Box is a clickable rectangle registered during View. Coordinates are
// absolute terminal cells (0,0 = top-left), matching mouse messages.
type Box struct {
	X, Y, W, H int
	ID         string
	Data       any
}

func (b Box) contains(x, y int) bool {
	return x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H
}

// Hitboxes is rebuilt on every View pass, so stale boxes are impossible.
type Hitboxes struct {
	boxes []Box
}

// Reset clears all boxes (start of a View pass).
func (h *Hitboxes) Reset() {
	h.boxes = h.boxes[:0]
}

// Add registers a box. Later additions win on overlap (topmost-last).
func (h *Hitboxes) Add(b Box) {
	h.boxes = append(h.boxes, b)
}

// At returns the topmost (last-registered) box containing the cell.
func (h *Hitboxes) At(x, y int) (Box, bool) {
	for i := len(h.boxes) - 1; i >= 0; i-- {
		if h.boxes[i].contains(x, y) {
			return h.boxes[i], true
		}
	}
	return Box{}, false
}
