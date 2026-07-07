// Package testutil provides an in-process fake game SSH server for bridge
// and router integration tests (the doc 05 harness). It records the
// username, wire key, env, PTY request, and window changes it receives,
// and runs a scriptable session handler.
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// SessionRecord captures what one bridged connection presented to the game.
type SessionRecord struct {
	User    string
	WireKey gossh.PublicKey // the key that authenticated (proxy key when bridged)
	Env     map[string]string
	Term    string
	Width   int
	Height  int

	mu      sync.Mutex
	winches [][2]int // {width, height}
}

func (r *SessionRecord) addWinch(w, h int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.winches = append(r.winches, [2]int{w, h})
}

// Winches returns recorded window changes as {width, height} pairs.
func (r *SessionRecord) Winches() [][2]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]int(nil), r.winches...)
}

// Script drives the session once the shell starts. Read the player's bytes
// from ch, write output to ch; the returned int is sent as exit-status.
// Returning a negative value closes the channel without an exit-status
// (simulates a crash / deploy kill).
type Script func(rec *SessionRecord, ch gossh.Channel) int

// EchoScript copies input back to the player until EOF, then exits 0.
func EchoScript(_ *SessionRecord, ch gossh.Channel) int {
	_, _ = io.Copy(ch, ch)
	return 0
}

// FakeGame is a minimal golang.org/x/crypto/ssh server.
type FakeGame struct {
	Addr    string
	HostPub gossh.PublicKey

	script   Script
	listener net.Listener

	mu       sync.Mutex
	sessions []*SessionRecord
	wg       sync.WaitGroup
}

// StartFakeGame listens on an ephemeral localhost port. script may be nil
// (defaults to EchoScript). The server stops on test cleanup.
func StartFakeGame(t *testing.T, script Script) *FakeGame {
	t.Helper()
	if script == nil {
		script = EchoScript
	}
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

	f := &FakeGame{script: script, HostPub: hostSigner.PublicKey()}

	cfg := &gossh.ServerConfig{
		PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			return &gossh.Permissions{
				Extensions: map[string]string{"wire-key": string(gossh.MarshalAuthorizedKey(key))},
			}, nil
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.listener = ln
	f.Addr = ln.Addr().String()

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				f.handleConn(conn, cfg)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		f.wg.Wait()
	})
	return f
}

// Stop closes the listener and waits for handlers (simulates deploy kill).
func (f *FakeGame) Stop() {
	_ = f.listener.Close()
	f.wg.Wait()
}

// Sessions returns records for every session that reached the shell.
func (f *FakeGame) Sessions() []*SessionRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*SessionRecord(nil), f.sessions...)
}

func (f *FakeGame) handleConn(conn net.Conn, cfg *gossh.ServerConfig) {
	defer conn.Close()
	sconn, chans, reqs, err := gossh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go gossh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(gossh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		rec := &SessionRecord{User: sconn.User(), Env: make(map[string]string)}
		if wk := sconn.Permissions.Extensions["wire-key"]; wk != "" {
			key, _, _, _, err := gossh.ParseAuthorizedKey([]byte(wk))
			if err == nil {
				rec.WireKey = key
			}
		}
		f.handleSession(rec, ch, chReqs)
	}
}

type ptyReqPayload struct {
	Term          string
	Width, Height uint32
	PixelW, PixelH uint32
	Modes         string
}

type envReqPayload struct{ Name, Value string }

type winchPayload struct {
	Width, Height  uint32
	PixelW, PixelH uint32
}

type exitStatusPayload struct{ Status uint32 }

func (f *FakeGame) handleSession(rec *SessionRecord, ch gossh.Channel, reqs <-chan *gossh.Request) {
	defer ch.Close()
	started := false
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p ptyReqPayload
			ok := gossh.Unmarshal(req.Payload, &p) == nil
			if ok {
				rec.Term = p.Term
				rec.Width, rec.Height = int(p.Width), int(p.Height)
			}
			replyIf(req, ok)
		case "env":
			var p envReqPayload
			ok := gossh.Unmarshal(req.Payload, &p) == nil
			if ok {
				rec.Env[p.Name] = p.Value
			}
			replyIf(req, ok)
		case "window-change":
			var p winchPayload
			ok := gossh.Unmarshal(req.Payload, &p) == nil
			if ok {
				rec.addWinch(int(p.Width), int(p.Height))
			}
			replyIf(req, ok)
		case "shell":
			replyIf(req, !started)
			if started {
				continue
			}
			started = true
			f.mu.Lock()
			f.sessions = append(f.sessions, rec)
			f.mu.Unlock()
			go func() {
				status := f.script(rec, ch)
				if status >= 0 {
					_, _ = ch.SendRequest("exit-status", false,
						gossh.Marshal(exitStatusPayload{Status: uint32(status)})) //nolint:gosec
				}
				_ = ch.Close()
			}()
		default:
			replyIf(req, false)
		}
	}
}

func replyIf(req *gossh.Request, ok bool) {
	if req.WantReply {
		_ = req.Reply(ok, nil)
	}
}

// StartSilentListener accepts TCP connections but never speaks SSH — a hung
// container. Returns its address.
func StartSilentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open, say nothing.
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// StartBannerListener speaks an SSH version banner and nothing else: the
// prober sees it Online, but a real handshake against it fails.
func StartBannerListener(t *testing.T) string {
	t.Helper()
	return StartCustomBannerListener(t, "SSH-2.0-fakebanner\r\n")
}

// StartCustomBannerListener speaks the given raw banner line (include the
// trailing "\r\n") and nothing else — for tests exercising the prober's
// fleet-version extraction from real-shaped and non-version-shaped banners.
func StartCustomBannerListener(t *testing.T, banner string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = fmt.Fprint(conn, banner)
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}
