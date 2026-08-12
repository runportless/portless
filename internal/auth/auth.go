package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const SessionCookie = "portless_session"

const sessionsFile = "browser-sessions.json"

type claim struct {
	next      string
	expiresAt time.Time
}

type session struct {
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Principal struct {
	Actor   string
	Session bool
	CSRF    string
}

type Manager struct {
	mu          sync.Mutex
	token       string
	claims      map[string]claim
	sessions    map[string]session
	sessionPath string
	now         func() time.Time
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
	manager := &Manager{
		token: value, claims: make(map[string]claim), sessions: make(map[string]session),
		sessionPath: filepath.Join(filepath.Dir(tokenPath), sessionsFile), now: time.Now,
	}
	if err := manager.loadSessions(); err != nil {
		return nil, err
	}
	manager.mu.Lock()
	if manager.cleanupLocked() {
		err = manager.saveSessionsLocked()
	}
	manager.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return manager, nil
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
	return Principal{Actor: "UI", Session: true, CSRF: current.CSRF}, true
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
	m.sessions[sessionToken] = session{CSRF: csrf, ExpiresAt: expiresAt}
	if err = m.saveSessionsLocked(); err != nil {
		delete(m.sessions, sessionToken)
		return
	}
	return
}

func (m *Manager) Logout(request *http.Request) error {
	cookie, err := request.Cookie(SessionCookie)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[cookie.Value]; !exists {
		return nil
	}
	delete(m.sessions, cookie.Value)
	return m.saveSessionsLocked()
}

func (m *Manager) cleanupLocked() bool {
	changed := false
	now := m.now()
	for code, claim := range m.claims {
		if !claim.expiresAt.After(now) {
			delete(m.claims, code)
		}
	}
	for token, session := range m.sessions {
		if !session.ExpiresAt.After(now) {
			delete(m.sessions, token)
			changed = true
		}
	}
	return changed
}

type sessionDocument struct {
	Version  int                `json:"version"`
	Sessions map[string]session `json:"sessions"`
}

func (m *Manager) loadSessions() error {
	info, err := os.Lstat(m.sessionPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect browser sessions: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("browser sessions path %s is not a regular file", m.sessionPath)
	}
	if err := os.Chmod(m.sessionPath, 0o600); err != nil {
		return fmt.Errorf("protect browser sessions: %w", err)
	}
	content, err := os.ReadFile(m.sessionPath)
	if err != nil {
		return fmt.Errorf("read browser sessions: %w", err)
	}
	if len(content) > 1<<20 {
		return errors.New("browser sessions file is unexpectedly large")
	}
	var document sessionDocument
	if err := json.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("decode browser sessions: %w", err)
	}
	if document.Version != 1 {
		return fmt.Errorf("unsupported browser sessions version %d", document.Version)
	}
	if document.Sessions == nil {
		document.Sessions = make(map[string]session)
	}
	for token, current := range document.Sessions {
		if strings.TrimSpace(token) == "" || strings.TrimSpace(current.CSRF) == "" || current.ExpiresAt.IsZero() {
			return errors.New("browser sessions file contains an invalid session")
		}
	}
	m.sessions = document.Sessions
	return nil
}

func (m *Manager) saveSessionsLocked() error {
	content, err := json.MarshalIndent(sessionDocument{Version: 1, Sessions: m.sessions}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode browser sessions: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.sessionPath), ".browser-sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("create browser sessions file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary browser sessions: %w", err)
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		return fmt.Errorf("write browser sessions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync browser sessions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close browser sessions: %w", err)
	}
	if err := os.Rename(temporaryPath, m.sessionPath); err != nil {
		return fmt.Errorf("publish browser sessions: %w", err)
	}
	return os.Chmod(m.sessionPath, 0o600)
}

func isControlOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "portless.localhost"
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
