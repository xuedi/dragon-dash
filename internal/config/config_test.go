package config

import (
	"path/filepath"
	"testing"
)

func newTemp(t *testing.T) *Config {
	t.Helper()
	c, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMissingFileIsNotAnError(t *testing.T) {
	c := newTemp(t)
	if got := c.Get("anything"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSetPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("core.prometheus_url", "http://example:9090"); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get("core.prometheus_url"); got != "http://example:9090" {
		t.Fatalf("got %q after reload", got)
	}
}

func TestSetEmptyDeletes(t *testing.T) {
	c := newTemp(t)
	_ = c.Set("k", "v")
	_ = c.Set("k", "")
	for _, k := range c.Keys() {
		if k == "k" {
			t.Fatal("empty value should remove the key")
		}
	}
}

func TestUnsetSystemIsEnabled(t *testing.T) {
	// A fresh install must show its systems, not an empty shell.
	c := newTemp(t)
	if !c.Enabled("dragon") {
		t.Fatal("an unconfigured system should default to enabled")
	}
	if err := c.SetEnabled("dragon", false); err != nil {
		t.Fatal(err)
	}
	if c.Enabled("dragon") {
		t.Fatal("should be disabled after SetEnabled(false)")
	}
}

func TestScopeIsolatesSystems(t *testing.T) {
	c := newTemp(t)
	a, b := c.Scoped("dragon"), c.Scoped("fritzhome")
	if err := a.Set("secret", "from-dragon"); err != nil {
		t.Fatal(err)
	}
	if got := b.Get("secret"); got != "" {
		t.Fatalf("fritzhome read dragon's key: %q", got)
	}
	if got := c.Get("system.dragon.secret"); got != "from-dragon" {
		t.Fatalf("scoped key stored at the wrong path: %q", got)
	}
}

func TestBoolAcceptsTheFormsTheSettingsPageWrites(t *testing.T) {
	c := newTemp(t)
	for _, v := range []string{"1", "true", "on"} {
		_ = c.Set("k", v)
		if !c.Bool("k") {
			t.Fatalf("%q should be true", v)
		}
	}
	for _, v := range []string{"0", "false", "no"} {
		_ = c.Set("k", v)
		if c.Bool("k") {
			t.Fatalf("%q should be false", v)
		}
	}
}
