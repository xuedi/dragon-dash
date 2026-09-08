// Package config loads read-only configuration from env files and the
// environment.
//
// Nothing here is writable at runtime. That is the point: with no write path
// there is no settings form to protect, which is what makes running on a LAN
// without authentication defensible. The app can be pointed somewhere new only
// by editing a file and restarting it.
//
// Sources are applied in order, each overriding the last:
//
//	.env.dist    committed defaults
//	.env.local   gitignored, holds credentials
//	environment  wins over both, so containers and systemd need no files
package config

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Prefix keeps our variables out of the way of everything else in the
// environment.
const Prefix = "DD_"

type source string

const (
	SourceDist source = ".env.dist"
	SourceEnv  source = "environment"
)

type value struct {
	Value  string
	Source source
}

type Config struct {
	values map[string]value
}

// Load reads the given env files in order, then overlays the process
// environment. A missing file is not an error: a deployment may configure
// everything through real environment variables.
func Load(files ...string) (*Config, error) {
	c := &Config{values: map[string]value{}}
	for _, f := range files {
		if err := c.loadFile(f); err != nil {
			return nil, err
		}
	}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, Prefix) {
			continue
		}
		c.values[k] = value{Value: v, Source: SourceEnv}
	}
	return c, nil
}

func (c *Config) loadFile(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		k, v, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, line)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Quotes are stripped so a value may contain spaces or a leading #.
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if !strings.HasPrefix(k, Prefix) {
			return fmt.Errorf("%s:%d: %q must start with %s", path, line, k, Prefix)
		}
		c.values[k] = value{Value: v, Source: source(path)}
	}
	return sc.Err()
}

// envName turns a dotted key into its environment variable name:
// system.fritzhome.metric_prefix becomes DD_SYSTEM_FRITZHOME_METRIC_PREFIX.
func envName(key string) string {
	return Prefix + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
}

// EnvName exposes the variable name a dotted key maps to, so the settings page
// can tell the reader exactly what to put in a file.
func EnvName(key string) string { return envName(key) }

func (c *Config) Get(key string) string { return c.values[envName(key)].Value }

func (c *Config) GetOr(key, def string) string {
	if v := c.Get(key); v != "" {
		return v
	}
	return def
}

func (c *Config) Bool(key string) bool {
	switch strings.ToLower(c.Get(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Source reports which file or environment a key came from, for the settings
// page. Knowing a value came from .env.dist rather than .env.local is usually
// the answer when something is unexpectedly empty.
func (c *Config) Source(key string) string {
	v, ok := c.values[envName(key)]
	if !ok {
		return ""
	}
	return string(v.Source)
}

func (c *Config) Has(key string) bool {
	_, ok := c.values[envName(key)]
	return ok
}

// Keys returns every set variable name, sorted.
func (c *Config) Keys() []string {
	out := make([]string, 0, len(c.values))
	for k := range c.values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Enabled reports whether a system should be shown. Unset means enabled, so a
// fresh checkout shows everything rather than an empty shell.
func (c *Config) Enabled(systemID string) bool {
	key := "system." + systemID + ".enabled"
	if !c.Has(key) {
		return true
	}
	return c.Bool(key)
}

// Scoped restricts a system to its own namespace so it cannot read another
// system's credentials by accident.
func (c *Config) Scoped(systemID string) *Scope {
	return &Scope{c: c, prefix: "system." + systemID + "."}
}

type Scope struct {
	c      *Config
	prefix string
}

func (s *Scope) Get(key string) string      { return s.c.Get(s.prefix + key) }
func (s *Scope) GetOr(k, def string) string { return s.c.GetOr(s.prefix+k, def) }
func (s *Scope) Bool(key string) bool       { return s.c.Bool(s.prefix + key) }
func (s *Scope) Source(key string) string   { return s.c.Source(s.prefix + key) }
func (s *Scope) EnvName(key string) string  { return envName(s.prefix + key) }
