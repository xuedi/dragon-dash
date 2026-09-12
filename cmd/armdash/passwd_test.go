package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"armdash/internal/auth"
)

func runPasswd(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, _ = w.WriteString(input)
	w.Close()
	var out, msg bytes.Buffer
	err = passwd(args, r, &out, &msg)
	return out.String(), err
}

func TestPasswdPrintsBothLines(t *testing.T) {
	out, err := runPasswd(t, "\ncorrect horse\ncorrect horse\n")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != "AD_CORE_AUTH_USER=admin" {
		t.Fatalf("output %q, want the default user and a hash", out)
	}
	hash, ok := strings.CutPrefix(lines[1], "AD_CORE_AUTH_PASSWORD_HASH=")
	if !ok || !auth.Verify("correct horse", hash) {
		t.Errorf("%q does not verify the password", lines[1])
	}
}

func TestPasswdRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		input string
		args  []string
	}{
		"mismatch":  {"correct horse\ncorrect horsE\n", []string{"owner"}},
		"too short": {"short\nshort\n", []string{"owner"}},
		"no input":  {"", []string{"owner"}},
		"bad name":  {"", []string{"a b"}},
	} {
		if out, err := runPasswd(t, c.input, c.args...); err == nil || out != "" {
			t.Errorf("%s: err %v, output %q, want an error and nothing on stdout", name, err, out)
		}
	}
}
