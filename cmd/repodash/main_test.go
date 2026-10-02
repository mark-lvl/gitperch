package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestInvocation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"--version"}, 0, "repodash dev"},
		{[]string{"--unknown"}, 2, "flag provided but not defined"},
		{[]string{"unknown"}, 2, "unexpected argument"},
	} {
		var out, errOut bytes.Buffer
		if got := run(tc.args, &out, &errOut); got != tc.code {
			t.Fatalf("%v: exit %d, want %d", tc.args, got, tc.code)
		}
		if !strings.Contains(out.String()+errOut.String(), tc.text) {
			t.Fatalf("%v: missing %q", tc.args, tc.text)
		}
	}
}
