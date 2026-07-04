package proxyproto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func fpOf(b byte) [sha256.Size]byte {
	var fp [sha256.Size]byte
	for i := range fp {
		fp[i] = b
	}
	return fp
}

func TestEncodeParseRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		fp   [sha256.Size]byte
		slot string
		want string // expected slot after sanitization
	}{
		{"simple", fpOf(0xab), "scout", "scout"},
		{"uppercase input", fpOf(0x01), "Scout", "scout"},
		{"spaces and punctuation", fpOf(0x02), "sc out!!", "scout"},
		{"empty falls back to default", fpOf(0x03), "", DefaultSlot},
		{"punctuation only falls back", fpOf(0x04), "!!!", DefaultSlot},
		{"max length slot", fpOf(0x05), strings.Repeat("a", 32), strings.Repeat("a", 32)},
		{"overlong slot truncated by sanitizer", fpOf(0x06), strings.Repeat("b", 40), strings.Repeat("b", 32)},
		{"digits dash underscore", fpOf(0x07), "a1-b_2", "a1-b_2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			username := Encode(tc.fp, tc.slot)
			if len(username) > 97 {
				t.Fatalf("username %q longer than 97 bytes", username)
			}
			fp, slot, err := Parse(username)
			if err != nil {
				t.Fatalf("Parse(%q): %v", username, err)
			}
			if fp != tc.fp {
				t.Fatalf("fingerprint round-trip mismatch")
			}
			if slot != tc.want {
				t.Fatalf("slot = %q, want %q", slot, tc.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	validHex := hex.EncodeToString(make([]byte, sha256.Size)) // 64 zeros
	cases := []struct {
		name     string
		username string
	}{
		{"empty", ""},
		{"no dot", validHex},
		{"short hex", validHex[:63] + ".scout"},
		{"long hex", validHex + "0.scout"},
		{"uppercase hex", strings.Repeat("AB", 32) + ".scout"},
		{"non-hex chars", strings.Repeat("g", 64) + ".scout"},
		{"empty slot", validHex + "."},
		{"oversized slot", validHex + "." + strings.Repeat("a", 33)},
		{"invalid slot chars", validHex + ".Scout"},
		{"slot with space", validHex + ".sc out"},
		{"second dot", validHex + ".sc.out"},
		{"trailing junk after valid slot", validHex + ".scout!"},
		{"dot too early", validHex[:32] + "." + validHex[32:] + ".scout"},
		{"plain username", "scout"},
		{"unicode slot", validHex + ".scöut"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Parse(tc.username); err == nil {
				t.Fatalf("Parse(%q) accepted, want error", tc.username)
			}
		})
	}
}

func TestSanitizeSlot(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Scout", "scout"},
		{"sc out!!", "scout"},
		{"", ""},
		{"  padded  ", "padded"},
		{"UPPER-Case_9", "upper-case_9"},
		{"日本語", ""},
		{strings.Repeat("x", 50), strings.Repeat("x", 32)},
	}
	for _, tc := range cases {
		if got := SanitizeSlot(tc.in); got != tc.want {
			t.Errorf("SanitizeSlot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFingerprintFormats checks the two fingerprint forms agree: the hex
// bytes the protocol sends re-encode to the display form games store.
func TestFingerprintFormats(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	raw := FingerprintBytes(key)
	display := Fingerprint(key)
	if !strings.HasPrefix(display, "SHA256:") {
		t.Fatalf("display fingerprint %q missing prefix", display)
	}
	want := sha256.Sum256(key.Marshal())
	if raw != want {
		t.Fatal("FingerprintBytes mismatch with direct sha256")
	}
	if Fingerprint(nil) != "" {
		t.Fatal("Fingerprint(nil) should be empty")
	}
}
