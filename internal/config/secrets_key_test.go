package config

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// ALEXANDRIA_SECRETS_KEY: unset leaves stored service logins off and the
// server booting as before; set but unusable refuses to boot.
func TestSecretsKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	noFile := func(string) ([]byte, error) { return nil, errors.New("no such file") }
	file := func(content string) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(content), nil }
	}

	cases := []struct {
		name         string
		inline, path string
		read         func(string) ([]byte, error)
		wantKey      bool
		wantProblem  bool
	}{
		{name: "unset", read: noFile},
		{name: "inline", inline: good, read: noFile, wantKey: true},
		{name: "from a file", path: "/run/secrets/k", read: file(good + "\n"), wantKey: true},
		{name: "unreadable file", path: "/nope", read: noFile, wantProblem: true},
		{name: "empty file", path: "/empty", read: file("  \n"), wantProblem: true},
		{name: "placeholder", inline: "change-me", read: noFile, wantProblem: true},
		{name: "the admin token reused", inline: "test-env-token-0123456789abcdef", read: noFile, wantProblem: true},
		{name: "too short", inline: base64.StdEncoding.EncodeToString([]byte("short")), read: noFile, wantProblem: true},
	}
	for _, tc := range cases {
		key, problem := secretsKey(tc.inline, tc.path, "test-env-token-0123456789abcdef", tc.read)
		if (key != nil) != tc.wantKey || (problem != "") != tc.wantProblem {
			t.Errorf("%s: key=%v problem=%q", tc.name, key != nil, problem)
		}
		if problem != "" && strings.Contains(problem, good) {
			t.Errorf("%s: the key itself was put in the message", tc.name)
		}
	}
}
