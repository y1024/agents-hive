package acpserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	ErrSessionBridgeNotFound     = errors.New("acp session binding not found")
	ErrSessionBridgeUnauthorized = errors.New("acp session binding unauthorized")
	ErrSessionBridgeExpired      = errors.New("acp session binding expired")
)

type SessionBridgeOptions struct {
	IdleTTL time.Duration
	Now     func() time.Time
}

type SessionBridge struct {
	mu      sync.Mutex
	idleTTL time.Duration
	now     func() time.Time
	entries map[string]*sessionBridgeEntry
}

type sessionBridgeEntry struct {
	internalSessionID string
	userID            string
	token             string
	lastSeen          time.Time
}

func NewSessionBridge(opts SessionBridgeOptions) *SessionBridge {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &SessionBridge{
		idleTTL: opts.IdleTTL,
		now:     now,
		entries: make(map[string]*sessionBridgeEntry),
	}
}

func (b *SessionBridge) Bind(acpSessionID string, internalSessionID string, userID string) (string, error) {
	if acpSessionID == "" || internalSessionID == "" || userID == "" {
		return "", fmt.Errorf("acp session, internal session, and user id are required")
	}
	token, err := randomBridgeToken()
	if err != nil {
		return "", err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[acpSessionID] = &sessionBridgeEntry{
		internalSessionID: internalSessionID,
		userID:            userID,
		token:             token,
		lastSeen:          b.now(),
	}
	return token, nil
}

func (b *SessionBridge) Resolve(acpSessionID string, userID string, token string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[acpSessionID]
	if !ok {
		return "", ErrSessionBridgeNotFound
	}
	if b.expired(entry) {
		delete(b.entries, acpSessionID)
		return "", ErrSessionBridgeExpired
	}
	if entry.userID != userID || entry.token == "" || entry.token != token {
		return "", ErrSessionBridgeUnauthorized
	}
	entry.lastSeen = b.now()
	return entry.internalSessionID, nil
}

func (b *SessionBridge) RotateToken(acpSessionID string, userID string, token string) (string, error) {
	if _, err := b.Resolve(acpSessionID, userID, token); err != nil {
		return "", err
	}
	newToken, err := randomBridgeToken()
	if err != nil {
		return "", err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.entries[acpSessionID]
	if !ok {
		return "", ErrSessionBridgeNotFound
	}
	entry.token = newToken
	entry.lastSeen = b.now()
	return newToken, nil
}

func (b *SessionBridge) Cleanup() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	removed := 0
	for sid, entry := range b.entries {
		if b.expired(entry) {
			delete(b.entries, sid)
			removed++
		}
	}
	return removed
}

func (b *SessionBridge) expired(entry *sessionBridgeEntry) bool {
	return b.idleTTL > 0 && b.now().Sub(entry.lastSeen) > b.idleTTL
}

func randomBridgeToken() (string, error) {
	var b [24]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
