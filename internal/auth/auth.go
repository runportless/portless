package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const SessionCookie = "portless_session"

type claim struct {
	next      string
	expiresAt time.Time
}

type session struct {
	csrf      string
	expiresAt time.Time
}

type Principal struct {
	Actor   string
	Session bool
	CSRF    string
}

type Manager struct {
	mu       sync.Mutex
	token    string
	claims   map[string]claim
	sessions map[string]session
	now      func() time.Time
}

func LoadOrCreate(tokenPath string) (*Manager, error) {
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		return nil, err
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	value := strings.TrimSpace(string(tokenBytes))
	if value == "" {
		value, err = randomToken(32)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(tokenPath, []byte(value+"\n"), 0o600); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		return nil, err
	}
	return &Manager{token: value, claims: make(map[string]claim), sessions: make(map[string]session), now: time.Now}, nil
}

func (m *Manager) Token() string { return m.token }

func (m *Manager) Authenticate(request *http.Request) (Principal, bool) {
	authorization := request.Header.Get("Authorization")
	if strings.HasPrefix(authorization, "Bearer ") {
		candidate := strings.TrimPrefix(authorization, "Bearer ")
		if constantEqual(candidate, m.token) {
			return Principal{Actor: "CLI"}, true
		}
	}
	cookie, err := request.Cookie(SessionCookie)
	if err != nil {
		return Principal{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	current, ok := m.sessions[cookie.Value]
	if !ok {
		return Principal{}, false
	}
	return Principal{Actor: "UI", Session: true, CSRF: current.csrf}, true
}

func (m *Manager) ValidateMutation(request *http.Request, principal Principal) error {
	if !principal.Session {
		return nil
	}
	if !constantEqual(request.Header.Get("X-Portless-CSRF"), principal.CSRF) {
		return errors.New("missing or invalid CSRF token")
	}
	origin := request.Header.Get("Origin")
	if origin != "" && !isControlOrigin(origin) {
		return fmt.Errorf("unexpected Origin %q", origin)
	}
	if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return fmt.Errorf("unexpected Sec-Fetch-Site %q", site)
	}
	return nil
}

func (m *Manager) IssueClaim(next string) (string, time.Time, error) {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	code, err := randomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := m.now().Add(60 * time.Second)
	m.mu.Lock()
	m.cleanupLocked()
	m.claims[code] = claim{next: next, expiresAt: expiresAt}
	m.mu.Unlock()
	return code, expiresAt, nil
}

func (m *Manager) ConsumeClaim(code string) (sessionToken, csrf, next string, expiresAt time.Time, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	pending, ok := m.claims[code]
	if !ok {
		err = errors.New("claim is invalid, expired, or already used")
		return
	}
	delete(m.claims, code)
	sessionToken, err = randomToken(32)
	if err != nil {
		return
	}
	csrf, err = randomToken(24)
	if err != nil {
		return
	}
	expiresAt = m.now().Add(12 * time.Hour)
	next = pending.next
	m.sessions[sessionToken] = session{csrf: csrf, expiresAt: expiresAt}
	return
}

func (m *Manager) Logout(request *http.Request) {
	cookie, err := request.Cookie(SessionCookie)
	if err != nil {
		return
	}
	m.mu.Lock()
	delete(m.sessions, cookie.Value)
	m.mu.Unlock()
}

func (m *Manager) cleanupLocked() {
	now := m.now()
	for code, claim := range m.claims {
		if !claim.expiresAt.After(now) {
			delete(m.claims, code)
		}
	}
	for token, session := range m.sessions {
		if !session.expiresAt.After(now) {
			delete(m.sessions, token)
		}
	}
}

func isControlOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "http://portless.localhost:")
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func constantEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
