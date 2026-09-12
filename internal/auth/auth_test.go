package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashRoundTrip(t *testing.T) {
	h, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256:600000:") || strings.ContainsAny(h, "$=\"' ") {
		t.Errorf("hash %q has the wrong shape for an env file", h)
	}
	if err := Check(h); err != nil {
		t.Errorf("Check(own hash) = %v", err)
	}
	if !Verify("correct horse", h) {
		t.Error("the right password does not verify")
	}
	if Verify("correct horsE", h) || Verify("", h) {
		t.Error("a wrong password verifies")
	}
	if again, _ := Hash("correct horse"); again == h {
		t.Error("two hashes of one password are equal, the salt is not random")
	}
	if _, err := Hash("short"); err == nil {
		t.Error("a password under the minimum length was hashed")
	}
}

func TestMalformedHashesMatchNothing(t *testing.T) {
	h, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(h, ":")
	key := []byte(parts[3])
	key[5] ^= 1
	tampered := strings.Join([]string{parts[0], parts[1], parts[2], string(key)}, ":")
	if Verify("correct horse", tampered) {
		t.Error("a tampered key verifies")
	}

	for name, bad := range map[string]string{
		"empty":            "",
		"bcrypt":           "$2a$10$abcdefghijklmnopqrstuv",
		"other scheme":     "pbkdf2-sha1:" + strings.Join(parts[1:], ":"),
		"missing key":      strings.Join(parts[:3], ":"),
		"extra field":      h + ":x",
		"iterations low":   strings.Join([]string{parts[0], "1000", parts[2], parts[3]}, ":"),
		"iterations high":  strings.Join([]string{parts[0], "99999999", parts[2], parts[3]}, ":"),
		"iterations word":  strings.Join([]string{parts[0], "many", parts[2], parts[3]}, ":"),
		"truncated salt":   strings.Join([]string{parts[0], parts[1], parts[2][:10], parts[3]}, ":"),
		"truncated key":    strings.Join([]string{parts[0], parts[1], parts[2], parts[3][:20]}, ":"),
		"padded base64":    strings.Join([]string{parts[0], parts[1], parts[2] + "==", parts[3]}, ":"),
		"standard base64+": strings.Join([]string{parts[0], parts[1], parts[2], "+" + parts[3][1:]}, ":"),
	} {
		if Check(bad) == nil {
			t.Errorf("%s: Check accepted %q", name, bad)
		}
		if Verify("correct horse", bad) {
			t.Errorf("%s: Verify matched %q", name, bad)
		}
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)} }
func (c *clock) sessions() *Sessions { s := NewSessions(SessionTTL); s.now = c.now; return s }
func (c *clock) limiter(max int) *Limiter {
	l := NewLimiter(max, 15*time.Minute, 15*time.Minute)
	l.now = c.now
	return l
}

func TestSessionsExpireOnTheServer(t *testing.T) {
	c := newClock()
	s := c.sessions()
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 64 || !s.Valid(id) {
		t.Fatalf("new session %q is not valid", id)
	}
	if s.Valid("") || s.Valid(strings.Repeat("0", 64)) {
		t.Error("an unknown session is valid")
	}
	c.add(SessionTTL - time.Second)
	if !s.Valid(id) {
		t.Error("the session expired early")
	}
	c.add(time.Second)
	if s.Valid(id) {
		t.Error("the session outlived its lifetime")
	}

	other, _ := s.Create()
	s.Delete(other)
	if s.Valid(other) {
		t.Error("a deleted session is still valid")
	}
}

func TestLimiterLocksAndReleases(t *testing.T) {
	c := newClock()
	l := c.limiter(3)
	const ip = "192.0.2.7"
	for i := range 2 {
		if l.Fail(ip) {
			t.Fatalf("locked after %d failures, want 3", i+1)
		}
	}
	if ok, _ := l.Allowed(ip); !ok {
		t.Fatal("locked before the limit")
	}
	if !l.Fail(ip) {
		t.Fatal("the third failure did not lock")
	}
	if ok, wait := l.Allowed(ip); ok || wait != 15*time.Minute {
		t.Fatalf("Allowed = %v, %v, want a 15 minute lock", ok, wait)
	}
	if ok, _ := l.Allowed("192.0.2.8"); !ok {
		t.Error("another address is locked too")
	}
	c.add(15 * time.Minute)
	if ok, _ := l.Allowed(ip); !ok {
		t.Error("the lock did not lift")
	}
	if l.Fail(ip) {
		t.Error("the count did not start over after the lock")
	}
}

func TestLimiterForgetsOldFailures(t *testing.T) {
	c := newClock()
	l := c.limiter(3)
	l.Fail("a")
	l.Fail("a")
	c.add(16 * time.Minute)
	if l.Fail("a") {
		t.Error("failures older than the window still count")
	}
	l.Fail("a")
	l.Reset("a")
	if l.Fail("a") || l.Fail("a") {
		t.Error("a successful login did not clear the count")
	}
}
