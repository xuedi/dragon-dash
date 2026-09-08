// Package config is a small persistent key/value store backed by one JSON file.
//
// Deliberately not SQLite: everything kept here is configuration, a handful of
// strings, and a plain JSON file is transparent, trivially backed up, editable
// by hand when something goes wrong, and keeps the binary on the standard
// library alone. If dragon-dash ever needs to store real data it should go to
// Prometheus, not here.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Config struct {
	path   string
	mu     sync.RWMutex
	values map[string]string
}

// Load reads path, creating an empty config if it does not exist.
func Load(path string) (*Config, error) {
	c := &Config{path: path, values: map[string]string{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &c.values); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return c, nil
}

func (c *Config) Get(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.values[key]
}

// GetOr returns the stored value, or def when unset.
func (c *Config) GetOr(key, def string) string {
	if v := c.Get(key); v != "" {
		return v
	}
	return def
}

func (c *Config) Bool(key string) bool {
	v := c.Get(key)
	return v == "1" || v == "true" || v == "on"
}

func (c *Config) Set(key, value string) error {
	c.mu.Lock()
	if value == "" {
		delete(c.values, key)
	} else {
		c.values[key] = value
	}
	c.mu.Unlock()
	return c.save()
}

// Keys returns every set key, sorted. Useful for debugging and tests.
func (c *Config) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.values))
	for k := range c.values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// save writes to a temp file and renames, so an interrupted write cannot
// truncate a good config.
func (c *Config) save() error {
	c.mu.RLock()
	b, err := json.MarshalIndent(c.values, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(c.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := c.path + ".tmp"
	// 0600: this file holds the FRITZ!Box password.
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Scoped returns a view restricted to one system's namespace. Passed to
// systems so a system cannot read another system's secrets by accident.
func (c *Config) Scoped(systemID string) *Scope {
	return &Scope{c: c, prefix: "system." + systemID + "."}
}

type Scope struct {
	c      *Config
	prefix string
}

func (s *Scope) Get(key string) string       { return s.c.Get(s.prefix + key) }
func (s *Scope) Set(key, value string) error { return s.c.Set(s.prefix+key, value) }
func (s *Scope) Bool(key string) bool        { return s.c.Bool(s.prefix + key) }
func (s *Scope) Prefix() string              { return s.prefix }
func (s *Scope) Has(key string) bool         { return s.Get(key) != "" }
func (s *Scope) TrimKey(full string) (string, bool) {
	return strings.CutPrefix(full, s.prefix)
}

// Enabled reports whether a system is switched on. Systems are always compiled
// in; this is the only thing that decides whether they appear.
func (c *Config) Enabled(systemID string) bool {
	// Unset means enabled: a fresh install shows everything rather than
	// presenting an empty shell with no clue what to do.
	v := c.Get("system." + systemID + ".enabled")
	return v == "" || v == "1" || v == "true"
}

func (c *Config) SetEnabled(systemID string, on bool) error {
	if on {
		return c.Set("system."+systemID+".enabled", "1")
	}
	return c.Set("system."+systemID+".enabled", "0")
}
