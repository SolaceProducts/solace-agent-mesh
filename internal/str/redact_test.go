// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"strings"
	"testing"
)

func TestRedactSensitive_EnvVarPatterns(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "api key",
			input: "OPENAI_API_KEY=sk-abc123secret",
			want:  "OPENAI_API_KEY=[REDACTED]",
		},
		{
			name:  "secret suffix",
			input: "MY_SECRET=mysecretvalue",
			want:  "MY_SECRET=[REDACTED]",
		},
		{
			name:  "secret prefix and suffix",
			input: "AWS_SECRET_KEY=secretval",
			want:  "AWS_SECRET_KEY=[REDACTED]",
		},
		{
			name:  "token",
			input: "GITHUB_TOKEN=ghp_abc123",
			want:  "GITHUB_TOKEN=[REDACTED]",
		},
		{
			name:  "password",
			input: "DB_PASSWORD=p@ssw0rd",
			want:  "DB_PASSWORD=[REDACTED]",
		},
		{
			name:  "tc prefix",
			input: "TC_MYSECRET=toolconfigval",
			want:  "TC_MYSECRET=[REDACTED]",
		},
		{
			name:  "tc underscore prefix",
			input: "TC_API_KEY=val123",
			want:  "TC_API_KEY=[REDACTED]",
		},
		{
			name:  "non-secret variable passes through",
			input: "SOME_VAR=notasecret",
			want:  "SOME_VAR=notasecret",
		},
		{
			name:  "path variable passes through",
			input: "PATH=/usr/bin:/bin",
			want:  "PATH=/usr/bin:/bin",
		},
		{
			name:  "multiline with secret and non-secret",
			input: "OPENAI_API_KEY=sk-abc123\nhello world\nMY_TOKEN=tok456",
			want:  "OPENAI_API_KEY=[REDACTED]\nhello world\nMY_TOKEN=[REDACTED]",
		},
		{
			name:  "inline with semicolon separator",
			input: "FOO=bar; MY_SECRET=secretval",
			want:  "FOO=bar; MY_SECRET=[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSensitive(tt.input)
			if got != tt.want {
				t.Errorf("redactSensitive(%q)\n  got  %q\n  want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactSensitive_BearerTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bearer token",
			input: "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig",
			want:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "bearer lowercase",
			input: "authorization: bearer mytoken123",
			want:  "authorization: bearer [REDACTED]",
		},
		{
			name:  "bearer mixed case",
			input: "BEARER MyToken",
			want:  "BEARER [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSensitive(tt.input)
			if got != tt.want {
				t.Errorf("redactSensitive(%q)\n  got  %q\n  want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactSensitive_BasicCredentials(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "basic credential",
			input: "Authorization: Basic c3ZjOmFwaS10b2tlbg==",
			want:  "Authorization: Basic [REDACTED]",
		},
		{
			name:  "basic lowercase",
			input: "authorization: basic dXNlcjpwYXNz",
			want:  "authorization: basic [REDACTED]",
		},
		{
			name:  "basic mixed case",
			input: "BASIC SomeBlob",
			want:  "BASIC [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSensitive(tt.input)
			if got != tt.want {
				t.Errorf("redactSensitive(%q)\n  got  %q\n  want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactSensitive_JWTs(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

	tests := []struct {
		name  string
		input string
		check func(string) bool
	}{
		{
			name:  "standalone jwt",
			input: "token: " + jwt,
			check: func(got string) bool { return !strings.Contains(got, jwt) && strings.Contains(got, "[REDACTED]") },
		},
		{
			name:  "jwt in env assignment",
			input: "MY_TOKEN=" + jwt,
			check: func(got string) bool { return !strings.Contains(got, jwt) && strings.Contains(got, "[REDACTED]") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSensitive(tt.input)
			if !tt.check(got) {
				t.Errorf("redactSensitive(%q) = %q — JWT not redacted", tt.input, got)
			}
		})
	}
}

func TestRedactSensitive_NoMatchPassesThrough(t *testing.T) {
	inputs := []string{
		"normal output line",
		"Traceback (most recent call last):",
		"  File tool.py, line 42",
		"ValueError: invalid input",
		"",
		"PATH=/usr/bin:/bin\nHOME=/root",
	}
	for _, input := range inputs {
		got := redactSensitive(input)
		if got != input {
			t.Errorf("redactSensitive(%q) = %q, want unchanged", input, got)
		}
	}
}

func TestRedactSensitive_Mixed(t *testing.T) {
	input := "Starting tool\nOPENAI_API_KEY=sk-secret\nAuthorization: Bearer tok123\nsome normal output\nTC_PRIVATE_KEY=privateval"
	got := redactSensitive(input)

	if strings.Contains(got, "sk-secret") {
		t.Error("sk-secret not redacted")
	}
	if strings.Contains(got, "tok123") {
		t.Error("bearer token not redacted")
	}
	if strings.Contains(got, "privateval") {
		t.Error("TC_ value not redacted")
	}
	if !strings.Contains(got, "Starting tool") {
		t.Error("non-secret line removed")
	}
	if !strings.Contains(got, "some normal output") {
		t.Error("non-secret line removed")
	}
}

func TestStderrTail(t *testing.T) {
	t.Run("empty returns empty", func(t *testing.T) {
		if got := stderrTail(""); got != "" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("short string returned as-is without truncation marker", func(t *testing.T) {
		s := "hello world"
		got := stderrTail(s)
		if got != s {
			t.Errorf("got %q, want %q", got, s)
		}
		if strings.Contains(got, "bytes truncated") {
			t.Error("truncation marker should not appear for short input")
		}
	})

	t.Run("long string includes truncation marker", func(t *testing.T) {
		var sb strings.Builder
		for range 200 {
			sb.WriteString("line of output that fills the buffer\n")
		}
		sb.WriteString("this is the last line")
		input := sb.String()
		got := stderrTail(input)
		if len(got) > stderrTailMaxBytes+100 {
			t.Errorf("tail length %d far exceeds max %d", len(got), stderrTailMaxBytes)
		}
		if !strings.Contains(got, "this is the last line") {
			t.Error("tail does not contain last line")
		}
		if !strings.Contains(got, "bytes truncated") {
			t.Error("truncation marker missing when input exceeds stderrTailMaxBytes")
		}
	})
}

func TestExtractStackTrace_PythonTraceback(t *testing.T) {
	input := `Some preamble
Traceback (most recent call last):
  File "/app/tool.py", line 42, in run
    result = do_thing()
  File "/app/lib.py", line 10, in do_thing
    raise ValueError("bad input")
ValueError: bad input
some trailing output`

	got := extractStackTrace(input)
	if !strings.Contains(got, "Traceback (most recent call last):") {
		t.Error("traceback header missing")
	}
	if !strings.Contains(got, "ValueError: bad input") {
		t.Error("exception line missing")
	}
}

func TestExtractStackTrace_GoPanic(t *testing.T) {
	input := `runtime output before
panic: runtime error: index out of range [5] with length 3

goroutine 1 [running]:
main.doWork(...)
	/app/main.go:15
main.main()
	/app/main.go:8 +0x5e

`
	got := extractStackTrace(input)
	if !strings.Contains(got, "panic:") {
		t.Error("panic line missing")
	}
	if !strings.Contains(got, "goroutine 1") {
		t.Error("goroutine line missing")
	}
}

func TestExtractStackTrace_NoMatch(t *testing.T) {
	inputs := []string{
		"normal output",
		"",
		"error: something went wrong",
	}
	for _, input := range inputs {
		if got := extractStackTrace(input); got != "" {
			t.Errorf("extractStackTrace(%q) = %q, want empty", input, got)
		}
	}
}

func TestExtractStackTrace_ChainedExceptions(t *testing.T) {
	input := `Traceback (most recent call last):
  File "/app/tool.py", line 10, in run
    connect()
  File "/app/db.py", line 5, in connect
    raise ConnectionError("db unreachable")
ConnectionError: db unreachable

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/tool.py", line 12, in run
    raise RuntimeError("tool failed")
RuntimeError: tool failed
`

	got := extractStackTrace(input)

	if !strings.Contains(got, "ConnectionError: db unreachable") {
		t.Error("first (root-cause) exception missing from stack_trace")
	}
	if !strings.Contains(got, "RuntimeError: tool failed") {
		t.Error("second (outer) exception missing from stack_trace")
	}
}

func TestExtractStackTrace_DirectCauseChain(t *testing.T) {
	input := `Traceback (most recent call last):
  File "/app/tool.py", line 3, in _inner
    raise ValueError("original")
ValueError: original

The above exception was the direct cause of the following exception:

Traceback (most recent call last):
  File "/app/tool.py", line 8, in run
    raise RuntimeError("wrapped") from exc
RuntimeError: wrapped
`

	got := extractStackTrace(input)

	if !strings.Contains(got, "ValueError: original") {
		t.Error("root-cause ValueError missing from stack_trace")
	}
	if !strings.Contains(got, "RuntimeError: wrapped") {
		t.Error("outer RuntimeError missing from stack_trace")
	}
}

func TestRedactSensitive_LongDottedIdentifierNotRedacted(t *testing.T) {
	cases := []string{
		"com.example.foo.bar.baz",
		"org.apache.kafka.clients.consumer.ConsumerRecord",
		"github.com.SolaceDev.solace-agent-mesh-go.internal.str",
	}
	for _, input := range cases {
		got := redactSensitive(input)
		if got != input {
			t.Errorf("redactSensitive(%q) = %q — dotted identifier incorrectly redacted", input, got)
		}
	}
}

func TestExtractStackTrace_TracebackBeforeLargeNoise(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Traceback (most recent call last):\n")
	sb.WriteString("  File \"/app/tool.py\", line 5, in run\n")
	sb.WriteString("    raise ValueError(\"root cause\")\n")
	sb.WriteString("ValueError: root cause\n")
	noise := strings.Repeat("x noise line\n", 8000)
	sb.WriteString(noise)

	fullStderr := sb.String()

	got := extractStackTrace(fullStderr)
	if !strings.Contains(got, "ValueError: root cause") {
		t.Error("stack_trace missing when traceback precedes large noise block")
	}
	tail := stderrTail(fullStderr)
	if strings.Contains(tail, "ValueError: root cause") {
		t.Log("note: traceback happened to survive tail truncation in this run")
	}
}
