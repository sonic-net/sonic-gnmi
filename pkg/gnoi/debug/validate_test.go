package debug

import (
	"errors"
	"strings"
	"testing"
)

var exampleWhitelist = []string{
	"echo",
	"ls",
	"cat",
	"tar",
	"sleep",
	"grep",
}

func TestValidateAndExtract(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		allow        bool
		expectedCmd  string
		expectedArgs []string
	}{
		{
			name:         "simple echo",
			input:        "echo hello world",
			allow:        true,
			expectedCmd:  "echo",
			expectedArgs: []string{"hello", "world"},
		},
		{
			name:         "simple ls",
			input:        "ls -la /tmp",
			allow:        true,
			expectedCmd:  "ls",
			expectedArgs: []string{"-la", "/tmp"},
		},
		{
			name:  "command substitution",
			input: "echo $(uname)",
			allow: false,
		},
		{
			name:  "not whitelisted command",
			input: "sh -c 'rm -rf /'",
			allow: false,
		},
		{
			name:  "inline assignment",
			input: "PATH=/tmp ls",
			allow: false,
		},
		{
			name:  "variable expansion",
			input: "echo \"$HOME\"",
			allow: false,
		},
		{
			name:  "absolute path",
			input: "/bin/ls -l",
			allow: false,
		},
		{
			name:  "redirect",
			input: "cat file1 > out",
			allow: false,
		},
		{
			name:  "backticks substitution",
			input: "echo `date`",
			allow: false,
		},
		{
			name:  "background operator",
			input: "sleep 1 &",
			allow: false,
		},
		{
			name:  "multiple statements",
			input: "echo a; echo b",
			allow: false,
		},
		{
			name:         "valid pipeline",
			input:        "ls | grep foo",
			allow:        true,
			expectedCmd:  "ls",
			expectedArgs: []string{"|", "grep", "foo"},
		},
		{
			name:         "valid pipeline - multiple chained statements",
			input:        "ls | echo foobar | grep foo",
			allow:        true,
			expectedCmd:  "ls",
			expectedArgs: []string{"|", "echo", "foobar", "|", "grep", "foo"},
		},
		{
			name:  "pipeline with empty statement",
			input: "ls | | grep foo",
			allow: false,
		},
		{
			name:  "pipeline with invalid command - command injection",
			input: "ls | rm -rf /",
			allow: false,
		},
		{
			name:  "malicious formatting - command injection",
			input: "ls \"-la;rm -rf /\"",
			allow: false,
		},
		{
			name:  "logical AND",
			input: "echo a && echo b",
			allow: false,
		},
		{
			name:  "logical OR",
			input: "echo a || echo b",
			allow: false,
		},
		{
			name:  "subshell",
			input: "(ls /)",
			allow: false,
		},
		{
			name:  "group commands",
			input: "{ echo a; echo b; }",
			allow: false,
		},
		{
			name:  "brace expansion",
			input: "echo {foo,bar}",
			allow: false,
		},
		{
			name:  "tilde expansion",
			input: "ls ~",
			allow: false,
		},
		{
			name:  "input redirect",
			input: "cat < /etc/hosts",
			allow: false,
		},
		{
			name:  "here string",
			input: "grep foo <<< 'bar'",
			allow: false,
		},
		{
			name:  "here document",
			input: "cat << EOF\nhello\nEOF",
			allow: false,
		},
		{
			name:  "arithmetic expansion",
			input: "echo $((1+1))",
			allow: false,
		},
		{
			name:  "process substitution",
			input: "diff <(ls /) <(ls /usr)",
			allow: false,
		},
		{
			name:  "empty input",
			input: "",
			allow: false,
		},
		{
			name:  "whitespace input",
			input: "   ",
			allow: false,
		},
		{
			name:  "leading semicolon",
			input: "; echo foo",
			allow: false,
		},
		{
			name:  "pipeline with malicious quoted arg",
			input: "ls | grep '; rm -rf /'",
			allow: false,
		},
		{
			name:  "pipeline with subshell",
			input: "ls | $(grep foo)",
			allow: false,
		},
		{
			name:  "glob wildcard",
			input: "ls *",
			allow: false,
		},
		{
			name:  "fuzz failure - slice index panic",
			input: "`$\\\\0",
			allow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCommand(tt.input, exampleWhitelist)

			if tt.allow {
				if err != nil {
					t.Fatalf("expected allow, got error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected rejection, but command was allowed: %s", tt.input)
				}
				if !errors.Is(err, ErrRejected) && !strings.Contains(err.Error(), "rejected") {
					t.Errorf("expected rejection error, got: %v", err)
				}
			}
		})
	}
}

// Variant of the above table tests, built to handle fuzzing. By default, just runs for the provided seed values.
// Can optionally run fuzzing as follows: `go test ./pkg/gnoi/debug -fuzz=FuzzValidateAndExtract -fuzztime=20s`
func FuzzValidateAndExtract(f *testing.F) {
	// Seed with known tricky patterns
	seeds := []string{
		"echo hello",
		"ls -l /tmp",
		"echo $(id)",
		"`uname`",
		"$(rm -rf /)",
		"ls | grep x",
		"sleep 1 &",
		"echo \"$USER\"",
		"cat <(ls)",
		"FOO=bar echo hi",
		"tar xf archive.tar",
		"echo \\; rm -rf /",
		"echo $(echo nested)",
		"echo ${HOME}",
		"$(echo)",
		"$((",
		"ls *.go",
		"echo > /tmp/x",
		"echo a && echo b",
		"echo a || echo b",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic for input %q: %v", input, r)
			}
		}()

		err := ValidateCommand(input, exampleWhitelist)

		// Allowed commands must obey invariants:
		if err == nil {
			args := strings.Split(input, " ")
			cmd := args[0]
			// 1. Command path must be in whitelist.
			if !sliceContains(exampleWhitelist, cmd) {
				t.Fatalf("allowed command %q not in whitelist (input=%q)", cmd, input)
			}

			// 2. Args must be non-empty literals, no dangerous symbols.
			for _, a := range args {
				if strings.ContainsAny(a, dangerousCharSet) {
					t.Errorf("allowed arg contains dangerous char: %q (input=%q)", a, input)
				}
			}
		} else {
			// For rejected inputs, ensure we don’t get false positives like parse errors leaking data
			if strings.Contains(strings.ToLower(err.Error()), "panic") {
				t.Errorf("unexpected panic-like error for input %q: %v", input, err)
			}
		}
	})
}
