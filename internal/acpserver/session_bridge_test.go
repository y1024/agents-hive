package acpserver

import (
	"testing"
	"time"
)

func TestSessionBridgeBindsSessionToUserAndToken(t *testing.T) {
	bridge := NewSessionBridge(SessionBridgeOptions{
		IdleTTL: time.Minute,
		Now:     fixedBridgeClock(time.Unix(100, 0)),
	})

	token, err := bridge.Bind("acp-1", "internal-1", "user-a")
	if err != nil {
		t.Fatalf("bind session: %v", err)
	}

	got, err := bridge.Resolve("acp-1", "user-a", token)
	if err != nil {
		t.Fatalf("resolve bound session: %v", err)
	}
	if got != "internal-1" {
		t.Fatalf("resolve internal session = %q, want internal-1", got)
	}
}

func TestSessionBridgeRejectsBareSessionHijack(t *testing.T) {
	bridge := NewSessionBridge(SessionBridgeOptions{
		IdleTTL: time.Minute,
		Now:     fixedBridgeClock(time.Unix(100, 0)),
	})

	token, err := bridge.Bind("acp-1", "internal-1", "user-a")
	if err != nil {
		t.Fatalf("bind session: %v", err)
	}

	if _, err := bridge.Resolve("acp-1", "user-b", token); err == nil {
		t.Fatal("resolve with different user unexpectedly succeeded")
	}
	if _, err := bridge.Resolve("acp-1", "user-a", ""); err == nil {
		t.Fatal("resolve without token unexpectedly succeeded")
	}
	if _, err := bridge.Resolve("acp-1", "user-a", "wrong-token"); err == nil {
		t.Fatal("resolve with wrong token unexpectedly succeeded")
	}
}

func TestSessionBridgeRotateTokenInvalidatesOldToken(t *testing.T) {
	now := time.Unix(100, 0)
	bridge := NewSessionBridge(SessionBridgeOptions{
		IdleTTL: time.Minute,
		Now:     fixedBridgeClock(now),
	})

	oldToken, err := bridge.Bind("acp-1", "internal-1", "user-a")
	if err != nil {
		t.Fatalf("bind session: %v", err)
	}

	newToken, err := bridge.RotateToken("acp-1", "user-a", oldToken)
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if newToken == oldToken {
		t.Fatal("rotated token should differ from old token")
	}
	if _, err := bridge.Resolve("acp-1", "user-a", oldToken); err == nil {
		t.Fatal("old token still resolved after rotation")
	}
	if _, err := bridge.Resolve("acp-1", "user-a", newToken); err != nil {
		t.Fatalf("new token did not resolve: %v", err)
	}
}

func TestSessionBridgeCleanupExpiresIdleSessions(t *testing.T) {
	now := time.Unix(100, 0)
	bridge := NewSessionBridge(SessionBridgeOptions{
		IdleTTL: time.Minute,
		Now: func() time.Time {
			return now
		},
	})

	token, err := bridge.Bind("acp-1", "internal-1", "user-a")
	if err != nil {
		t.Fatalf("bind session: %v", err)
	}
	now = now.Add(2 * time.Minute)

	removed := bridge.Cleanup()
	if removed != 1 {
		t.Fatalf("cleanup removed %d sessions, want 1", removed)
	}
	if _, err := bridge.Resolve("acp-1", "user-a", token); err == nil {
		t.Fatal("expired session still resolved after cleanup")
	}
}

func fixedBridgeClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}
