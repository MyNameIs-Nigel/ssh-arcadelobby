package server

import (
	"io"
	"sync"

	"github.com/charmbracelet/ssh"
)

// inputPump owns every read from the SSH session for the connection's
// lifetime and forwards the bytes to whichever side is active — the lobby
// program or the bridge.
//
// Why: the session handler alternates between a Bubble Tea program and a
// bridged game on the same session. Bubble Tea's input reader for a generic
// io.Reader is a fallback cancelreader whose Cancel cannot interrupt an
// in-flight Read — a quit lobby program would leave a blocked Read on the
// session that swallows the player's next keystroke mid-game. With the
// pump, consumers read from pipes we can close deterministically; nothing
// but the pump ever reads the session.
type inputPump struct {
	mu   sync.Mutex
	dst  io.Writer
	done chan struct{}
}

func newInputPump(src io.Reader) *inputPump {
	p := &inputPump{done: make(chan struct{})}
	go p.loop(src)
	return p
}

func (p *inputPump) loop(src io.Reader) {
	defer close(p.done)
	defer p.closeDest()
	buf := make([]byte, 1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			p.mu.Lock()
			dst := p.dst
			p.mu.Unlock()
			if dst != nil {
				// A write error means the destination was revoked mid-write
				// (mode switch) — drop the chunk, keep pumping. Bytes typed
				// between modes are noise by definition.
				_, _ = dst.Write(buf[:n])
			}
		}
		if err != nil {
			return
		}
	}
}

// SetDest routes subsequent input to w; nil discards. The previous
// destination is not closed — the handler owns its pipes and closes them
// after switching, which also unblocks any in-flight write.
func (p *inputPump) SetDest(w io.Writer) {
	p.mu.Lock()
	p.dst = w
	p.mu.Unlock()
}

// Done is closed when the session's input stream ends (player disconnect).
func (p *inputPump) Done() <-chan struct{} { return p.done }

// closeDest propagates session EOF to the active consumer so it shuts down
// (the lobby program's input reader, or the game via the bridge's stdin).
func (p *inputPump) closeDest() {
	p.mu.Lock()
	dst := p.dst
	p.mu.Unlock()
	if c, ok := dst.(io.Closer); ok {
		_ = c.Close()
	}
}

// winchPump owns the session's window-change channel for the connection's
// lifetime. The charm ssh library delivers resizes into a 1-buffered channel
// with a blocking send, so someone must always be draining it; the pump
// does, and hands events to whichever side is attached. Attach primes the
// new consumer with the latest size so a resize during a mode switch is
// never lost.
type winchPump struct {
	mu     sync.Mutex
	dst    chan ssh.Window
	latest ssh.Window
	has    bool
	done   chan struct{}
}

func newWinchPump(src <-chan ssh.Window) *winchPump {
	p := &winchPump{done: make(chan struct{})}
	go p.loop(src)
	return p
}

func (p *winchPump) loop(src <-chan ssh.Window) {
	defer close(p.done)
	for w := range src {
		p.mu.Lock()
		p.latest = w
		p.has = true
		dst := p.dst
		p.mu.Unlock()
		if dst != nil {
			deliver(dst, w)
		}
	}
	// Session over: release the attached consumer, if any.
	p.mu.Lock()
	dst := p.dst
	p.dst = nil
	p.mu.Unlock()
	if dst != nil {
		close(dst)
	}
}

// deliver replaces a stale pending event instead of blocking: resizes
// coalesce, latest wins.
func deliver(ch chan ssh.Window, w ssh.Window) {
	for {
		select {
		case ch <- w:
			return
		default:
			select {
			case <-ch:
			default:
			}
		}
	}
}

// Attach makes a fresh consumer channel, primed with the latest known size.
func (p *winchPump) Attach() <-chan ssh.Window {
	ch := make(chan ssh.Window, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		close(ch)
		return ch
	default:
	}
	p.dst = ch
	if p.has {
		ch <- p.latest
	}
	return ch
}

// Detach stops delivery. The old channel is left open and simply becomes
// garbage; its consumer goroutine has already exited by the time the
// handler detaches.
func (p *winchPump) Detach() {
	p.mu.Lock()
	p.dst = nil
	p.mu.Unlock()
}
