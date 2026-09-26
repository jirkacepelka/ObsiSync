// Package auth implements password hashing, opaque tokens and login throttling.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommended minimum; cheap enough for small boxes).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func CheckPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var ErrWeakPassword = errors.New("err.weakPassword") // i18n key

func ValidatePassword(pw string) error {
	if len([]rune(pw)) < 8 {
		return ErrWeakPassword
	}
	return nil
}

// NewToken returns a random opaque token and the hash to store in the database.
func NewToken(prefix string) (token, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Limiter counts failures per key within a sliding window.
type Limiter struct {
	mu      sync.Mutex
	fails   map[string][]time.Time
	max     int
	window  time.Duration
	maxKeys int
	lastGC  time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{fails: map[string][]time.Time{}, max: max, window: window, maxKeys: 100_000}
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

// Allowed reports whether another attempt may be made for key.
func (l *Limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.lastGC) > l.window {
		for k := range l.fails {
			l.prune(k, now)
		}
		l.lastGC = now
	}
	return len(l.prune(key, now)) < l.max
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Bounded memory: when flooded with made-up keys, new keys are not
	// tracked (the account-wide limit still applies to real accounts).
	if _, ok := l.fails[key]; !ok && len(l.fails) >= l.maxKeys {
		return
	}
	l.fails[key] = append(l.fails[key], time.Now())
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// LoginGuard protects password checks against guessing:
//   - 10 failures per IP address and name in 15 minutes,
//   - 30 failures per IP address in 15 minutes (trying many names),
//   - 30 failures per existing account in 15 minutes, whatever the IP
//     (a botnet or a spoofed X-Forwarded-For does not get around it).
//
// Devices and browser sessions that are already logged in keep working
// while an account is throttled.
//
// It also caps how many Argon2 checks run at once (each needs ~19 MB), and
// checks a dummy hash for unknown names so the response time does not
// reveal which names exist.
type LoginGuard struct {
	pair, ip, account *Limiter
	sem               chan struct{}
	dummy             string
}

func NewLoginGuard() *LoginGuard {
	const window = 15 * time.Minute
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	dummy, err := HashPassword("dummy password for unknown names")
	if err != nil {
		panic(err)
	}
	return &LoginGuard{
		pair:    NewLimiter(10, window),
		ip:      NewLimiter(30, window),
		account: NewLimiter(30, window),
		sem:     make(chan struct{}, n),
		dummy:   dummy,
	}
}

func accountKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Allowed reports whether a login attempt from ip for name may be checked.
func (g *LoginGuard) Allowed(ip, name string) bool {
	return g.pair.Allowed(ip+"|"+accountKey(name)) && g.ip.Allowed(ip) && g.account.Allowed(accountKey(name))
}

// Check verifies pw against encoded (empty for an unknown name) and records
// the result.
func (g *LoginGuard) Check(ip, name, encoded, pw string) bool {
	known := encoded != ""
	if !known {
		encoded = g.dummy
	}
	g.sem <- struct{}{}
	ok := CheckPassword(encoded, pw) && known
	<-g.sem
	pair := ip + "|" + accountKey(name)
	if ok {
		g.pair.Reset(pair)
		return true
	}
	g.pair.Fail(pair)
	g.ip.Fail(ip)
	if known {
		g.account.Fail(accountKey(name))
	}
	return false
}
