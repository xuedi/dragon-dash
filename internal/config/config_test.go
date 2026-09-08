package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMissingFileIsNotAnError(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.env"))
	if err != nil {
		t.Fatalf("a missing env file should be tolerated: %v", err)
	}
	if got := c.Get("core.prometheus_url"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestLaterFileWins(t *testing.T) {
	dir := t.TempDir()
	dist := write(t, dir, ".env.dist", "DD_CORE_PROMETHEUS_URL=http://dist:9090\n")
	local := write(t, dir, ".env.local", "DD_CORE_PROMETHEUS_URL=http://local:9090\n")
	c, err := Load(dist, local)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Get("core.prometheus_url"); got != "http://local:9090" {
		t.Fatalf("got %q, want the .env.local value", got)
	}
	if got := c.Source("core.prometheus_url"); got != local {
		t.Fatalf("source = %q, want %q", got, local)
	}
}

func TestEnvironmentBeatsFiles(t *testing.T) {
	dir := t.TempDir()
	dist := write(t, dir, ".env.dist", "DD_CORE_PROMETHEUS_URL=http://dist:9090\n")
	t.Setenv("DD_CORE_PROMETHEUS_URL", "http://env:9090")
	c, err := Load(dist)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Get("core.prometheus_url"); got != "http://env:9090" {
		t.Fatalf("got %q, want the environment value", got)
	}
	if got := c.Source("core.prometheus_url"); got != string(SourceEnv) {
		t.Fatalf("source = %q, want %q", got, SourceEnv)
	}
}

func TestParsing(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, ".env", `
# a comment
export DD_A=plain
DD_B = "quoted value"
DD_C='single'
DD_D=has=equals
`)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"a": "plain", "b": "quoted value", "c": "single", "d": "has=equals"} {
		if got := c.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestUnprefixedKeyIsRejected(t *testing.T) {
	// A typo like PROMETHEUS_URL= would otherwise be silently ignored, which
	// is a miserable thing to debug.
	dir := t.TempDir()
	f := write(t, dir, ".env", "PROMETHEUS_URL=http://x:9090\n")
	if _, err := Load(f); err == nil {
		t.Fatal("expected an error for a key without the DD_ prefix")
	}
}

func TestEnvNameMapping(t *testing.T) {
	if got := EnvName("system.fritzhome.metric_prefix"); got != "DD_SYSTEM_FRITZHOME_METRIC_PREFIX" {
		t.Fatalf("got %q", got)
	}
}

func TestUnsetSystemIsEnabled(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled("dragon") {
		t.Fatal("an unconfigured system should default to enabled")
	}
}

func TestSystemCanBeDisabled(t *testing.T) {
	t.Setenv("DD_SYSTEM_DRAGON_ENABLED", "0")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled("dragon") {
		t.Fatal("should be disabled")
	}
}

func TestScopeIsolatesSystems(t *testing.T) {
	t.Setenv("DD_SYSTEM_DRAGON_SECRET", "from-dragon")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Scoped("fritzhome").Get("secret"); got != "" {
		t.Fatalf("fritzhome read dragon's key: %q", got)
	}
	if got := c.Scoped("dragon").Get("secret"); got != "from-dragon" {
		t.Fatalf("got %q", got)
	}
}
