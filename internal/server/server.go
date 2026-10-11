// Package server is the public half of the router: a Wish SSH server that
// accepts anyone with a key (trust-on-first-use), shows the arcade menu,
// and hands sessions to the bridge. Player prefs (alpha-warning acks) live
// in SQLite; rate limiting here protects the whole fleet.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"charm.land/wish/v2"
	"charm.land/wish/v2/logging"
	"charm.land/wish/v2/ratelimiter"
	"github.com/charmbracelet/ssh"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/banner"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/config"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/presence"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
)

// Server wraps the Wish SSH server and related middleware.
type Server struct {
	cfg       config.Config
	logger    *slog.Logger
	ssh       *ssh.Server
	registry  *registry.Registry
	banner    *banner.Source
	store     accountStore
	authGuard *authGuard
	proxyKey  gossh.Signer
	presence  *presence.Tracker
}

// New constructs and configures the SSH server over the given registry.
func New(cfg config.Config, logger *slog.Logger, reg *registry.Registry, ban *banner.Source, st accountStore) (*Server, error) {
	for _, p := range []string{cfg.HostKeyPath, cfg.ProxyKeyPath} {
		if err := ensureKeyDir(p); err != nil {
			return nil, err
		}
	}

	proxyKey, err := loadOrCreateProxyKey(cfg.ProxyKeyPath, logger)
	if err != nil {
		return nil, err
	}

	limits := NewSessionLimits(cfg.MaxConnections, cfg.MaxSessionsPerKey, cfg.MaxGuestSessions, cfg.MaxGuestSessionsPerIP)
	rl := ratelimiter.NewRateLimiter(
		rate.Limit(cfg.RateLimitPerSecond),
		cfg.RateLimitBurst,
		cfg.RateLimitMaxEntries,
	)

	srv := &Server{
		cfg: cfg, logger: logger, registry: reg, banner: ban, store: st,
		proxyKey: proxyKey, presence: presence.NewTracker(),
		authGuard: newAuthGuard(cfg.MaxPendingAuth, cfg.MaxConnections+cfg.MaxPendingAuth, cfg.AuthTimeout),
	}

	srv.authGuard.maxGuests = cfg.MaxGuestSessions
	srv.authGuard.maxGuestsPerIP = cfg.MaxGuestSessionsPerIP

	// No idle timeout at the transport: the lobby enforces its own via the
	// UI, and during a bridged game only the game's idle rules apply.
	s, err := wish.NewServer(
		wish.WithAddress(cfg.ListenAddr()),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithPublicKeyAuth(publicKeyAuth),
		ssh.WrapConn(func(ctx ssh.Context, c net.Conn) net.Conn {
			guarded := srv.authGuard.wrap(c)
			if guarded == nil {
				return nil
			}
			ctx.SetValue(authConnContextKey{}, guarded)
			return guarded
		}),
		func(s *ssh.Server) error {
			s.ServerConfigCallback = func(ctx ssh.Context) *gossh.ServerConfig {
				return &gossh.ServerConfig{AuthLogCallback: func(_ gossh.ConnMetadata, _ string, err error) {
					if err == nil {
						if c, ok := ctx.Value(authConnContextKey{}).(*guardedConn); ok {
							c.authenticated()
						}
					}
				}}
			}
			if cfg.AllowGuests {
				s.KeyboardInteractiveHandler = guestAuth
			}
			return nil
		},
		// Middlewares run bottom-up: logging → rate limit → channel scope
		// → authenticated identity → caps → PTY → lobby/bridge loop.
		wish.WithMiddleware(
			srv.sessionLoop(),
			RequirePTY(),
			limits.Middleware(),
			resolveIdentity(cfg.AllowGuests),
			scopeSession(),
			ratelimiter.Middleware(rl),
			logging.Middleware(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create ssh server: %w", err)
	}

	srv.ssh = s
	return srv, nil
}

// sessionLoop adapts the handler to the middleware chain as its terminus.
func (srv *Server) sessionLoop() wish.Middleware {
	return func(ssh.Handler) ssh.Handler {
		return srv.handle
	}
}

// Presence is the live tally of who is in the menu and in which game — the
// source for the player-count snapshot (docs/07).
func (s *Server) Presence() *presence.Tracker {
	return s.presence
}

// ListenAndServe starts accepting SSH connections.
func (s *Server) ListenAndServe() error {
	s.logger.Info("ssh server listening", "addr", s.cfg.ListenAddr())
	return s.ssh.ListenAndServe()
}

// Shutdown stops accepting new connections and waits for existing ones.
// Games attempt their normal keyed save on disconnect; guest visits are
// discarded. A hard crash cannot guarantee that the latest state was saved.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("ssh server shutting down")
	s.authGuard.closePending()
	return s.ssh.Shutdown(ctx)
}

func ensureKeyDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

// loadOrCreateProxyKey loads the bridge client key, generating an ed25519
// keypair on first run in dev. In production the key is a mounted secret —
// whoever holds it can impersonate any player to any game; it lives only on
// the host, never in an image, never in git.
func loadOrCreateProxyKey(path string, logger *slog.Logger) (gossh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		signer, err := gossh.ParsePrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse proxy key %s: %w", path, err)
		}
		return signer, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read proxy key %s: %w", path, err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate proxy key: %w", err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "ssharcade proxy key")
	if err != nil {
		return nil, fmt.Errorf("marshal proxy key: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("write proxy key %s: %w", path, err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("proxy public key: %w", err)
	}
	pubPath := path + ".pub"
	if err := os.WriteFile(pubPath, gossh.MarshalAuthorizedKey(sshPub), 0o644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("write proxy public key %s: %w", pubPath, err)
	}
	logger.Warn("generated new proxy keypair — distribute the public half to every game",
		"key", path, "pub", pubPath)

	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("proxy signer: %w", err)
	}
	return signer, nil
}
