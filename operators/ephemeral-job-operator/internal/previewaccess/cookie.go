package previewaccess

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CookieName is the preview session cookie (ADR-108 §4). __Host- makes the
// browser enforce what the ADR states: Secure, Path=/, and no Domain, so it is
// bound to exactly one preview host.
const CookieName = "__Host-zo-preview"

// CookieMaxAge is the longest a preview cookie lives; never past the record's
// expiresAt.
const CookieMaxAge = 8 * time.Hour

// ticket is what the cookie carries, signed. Every field is checked on every
// request: the record's UID (a record deleted and recreated under the same name
// is a different preview), the host, the subject, the expiry, and the app
// origin a refusal page may post to.
type ticket struct {
	Record  string `json:"r"`
	Host    string `json:"h"`
	Subject string `json:"s"`
	Expiry  int64  `json:"e"`
	Origin  string `json:"o"`
}

// Signer signs and verifies tickets with the environment's preview key.
type Signer struct{ key []byte }

// NewSigner wraps a key, refusing one too short to be a MAC key.
func NewSigner(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("preview signing key is %d bytes; at least 32 are required", len(key))
	}
	return &Signer{key: key}, nil
}

func (s *Signer) mac(payload string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Signer) seal(t ticket) string {
	b, _ := json.Marshal(t)
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + s.mac(p)
}

// open returns the ticket if the value is one this service signed. It does NOT
// judge expiry or host: callers do, so a refusal can say which it was.
func (s *Signer) open(v string) (ticket, error) {
	p, sig, ok := strings.Cut(v, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(p))) {
		return ticket{}, errors.New("not a cookie this service issued")
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return ticket{}, err
	}
	var t ticket
	return t, json.Unmarshal(b, &t)
}

// cookie is the Set-Cookie for a redeemed ticket (ADR-108 §4).
func (s *Signer) cookie(t ticket, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:        CookieName,
		Value:       s.seal(t),
		Path:        "/",
		MaxAge:      int(maxAge / time.Second),
		Secure:      true,
		HttpOnly:    true,
		SameSite:    http.SameSiteNoneMode,
		Partitioned: true,
	}
}

// code is a single-use handoff (ADR-108 §3): 256 random bits, valid for
// CodeTTL, bound to one record, host, subject and app origin.
type code struct {
	ticket
	issued time.Time
}

// CodeTTL is how long a handoff code may wait to be redeemed.
const CodeTTL = 10 * time.Second

// Codes holds unredeemed codes. In memory, deliberately: a code lives ten
// seconds and is useless after a restart's ten seconds anyway, and keeping it
// off disk keeps it out of every backup. The service therefore runs as a single
// replica (ADR-108 Consequences).
type Codes struct {
	mu sync.Mutex
	m  map[string]code
}

// Issue mints a code for t.
func (c *Codes) Issue(t ticket) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	k := base64.RawURLEncoding.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]code{}
	}
	now := time.Now()
	for key, v := range c.m { // bounded by issue rate x 10 s
		if now.Sub(v.issued) > CodeTTL {
			delete(c.m, key)
		}
	}
	c.m[k] = code{ticket: t, issued: now}
	return k, nil
}

// Redeem consumes a code for host. A code is gone after its first redemption
// attempt, successful or not, so a replay finds nothing.
func (c *Codes) Redeem(k, host string) (ticket, error) {
	c.mu.Lock()
	v, ok := c.m[k]
	delete(c.m, k)
	c.mu.Unlock()
	switch {
	case !ok:
		return ticket{}, errors.New("unknown or already used code")
	case time.Since(v.issued) > CodeTTL:
		return ticket{}, errors.New("code expired")
	case v.Host != host:
		return ticket{}, errors.New("code was issued for another host")
	}
	return v.ticket, nil
}
