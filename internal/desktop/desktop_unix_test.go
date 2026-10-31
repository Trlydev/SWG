//go:build !windows

package desktop

import "testing"

// Exec paths are quoted as the desktop entry spec asks, so a path with
// %, $, quotes or backslashes stays one argument and changes nothing.
func TestExecQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/a/ppgmods":                `"/home/a/ppgmods"`,
		"/home/a/Downloads/100%/ppgmods": `"/home/a/Downloads/100%%/ppgmods"`,
		`/home/a/$x"y/ppg`:               `"/home/a/\\$x\\"y/ppg"`,
		`/home/a\b`:                      `"/home/a\\\\b"`,
	} {
		if got := execQuote(in); got != want {
			t.Errorf("execQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
