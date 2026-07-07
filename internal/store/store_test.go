package store

import (
	"context"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := t.TempDir() + "/arcade.db"
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSchemaMigrates(t *testing.T) {
	st := openTestStore(t)
	v, err := st.SchemaVersion(context.Background())
	if err != nil || v != 1 {
		t.Fatalf("schema version = %d err=%v", v, err)
	}
}

func TestTouchAccountAndAlphaAck(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	fp := "SHA256:abc"
	if err := st.TouchAccount(ctx, fp, "ssh-ed25519 AAAA", now); err != nil {
		t.Fatal(err)
	}
	ok, err := st.HasAlphaAck(ctx, fp, "moonminer")
	if err != nil || ok {
		t.Fatalf("unexpected ack before insert: ok=%v err=%v", ok, err)
	}
	if err := st.AckAlphaWarning(ctx, fp, "moonminer", now+1); err != nil {
		t.Fatal(err)
	}
	ok, err = st.HasAlphaAck(ctx, fp, "moonminer")
	if err != nil || !ok {
		t.Fatalf("ack not stored: ok=%v err=%v", ok, err)
	}
}

func TestAlphaAckIsPerGame(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	fp := "SHA256:player"
	if err := st.TouchAccount(ctx, fp, "ssh-ed25519 BBBB", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AckAlphaWarning(ctx, fp, "moonminer", now); err != nil {
		t.Fatal(err)
	}
	ok, err := st.HasAlphaAck(ctx, fp, "packetderby")
	if err != nil || ok {
		t.Fatalf("other game should not be acked: ok=%v err=%v", ok, err)
	}
}

func TestAlphaAckIsPerFingerprint(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()
	if err := st.AckAlphaWarning(ctx, "SHA256:one", "moonminer", now); err != nil {
		t.Fatal(err)
	}
	ok, err := st.HasAlphaAck(ctx, "SHA256:two", "moonminer")
	if err != nil || ok {
		t.Fatalf("other fingerprint should not be acked: ok=%v err=%v", ok, err)
	}
}

func TestInvalidKeysRejected(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.AckAlphaWarning(ctx, "", "moonminer", 1); err != ErrInvalidKey {
		t.Fatalf("err = %v", err)
	}
	if err := st.AckAlphaWarning(ctx, "SHA256:x", "BAD ID", 1); err != ErrInvalidKey {
		t.Fatalf("err = %v", err)
	}
}
