package judge

import (
	"testing"

	"judgex/internal/sandbox"
)

func TestNormalizeOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"blank only", "  \t\n  \n", ""},
		{"simple", "hello\n", "hello"},
		{"trailing spaces", "3\n  ", "3"},
		{"trailing blank lines", "3\n4\n\n\n", "3\n4"},
		{"CRLF", "3\r\n4\r\n", "3\n4"},
		{"mixed", "  hello world  \n\tfoo\t\n", "hello world\n\tfoo"},
		{"numbers", "1 2\n3 4\n", "1 2\n3 4"},
		{"single line no newline", "42", "42"},
		{"spaces within line", "a   b", "a   b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeOutput(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeOutput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCompareOutput(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		pass     bool
	}{
		{"identical", "3\n", "3\n", true},
		{"trailing diff ignored", "3\n", "3\n  ", true},
		{"blank lines ignored", "3\n", "3\n\n\n", true},
		{"real mismatch", "3\n", "4\n", false},
		{"empty vs blank", "", "\n  \n", true},
		{"both empty", "", "", true},
		{"multiline same", "1\n2\n3\n", "1\n2\n3\n", true},
		{"multiline diff", "1\n2\n3\n", "1\n2\n4\n", false},
		{"CRLF vs LF", "a\r\nb\r\n", "a\nb\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareOutput(tt.expected, tt.actual)
			got := err == nil
			if got != tt.pass {
				t.Errorf("CompareOutput(%q, %q) = %v, want pass=%v", tt.expected, tt.actual, err, tt.pass)
			}
		})
	}
}

func TestSandboxResultToJudgeResult(t *testing.T) {
	tests := []struct {
		name   string
		sr     *sandbox.RunResult
		wantSt string
	}{
		{
			name:   "accepted",
			sr:     &sandbox.RunResult{Status: "ok", Stdout: "3\n", TimeUsed: 100, MemoryUsed: 512},
			wantSt: StatusAccepted,
		},
		{
			name:   "tle",
			sr:     &sandbox.RunResult{Status: "tle", TimeUsed: 1000, MemoryUsed: 256},
			wantSt: StatusTimeLimitExceeded,
		},
		{
			name:   "mle",
			sr:     &sandbox.RunResult{Status: "mle", TimeUsed: 300, MemoryUsed: 131072},
			wantSt: StatusMemoryLimitExceeded,
		},
		{
			name:   "re segfault",
			sr:     &sandbox.RunResult{Status: "re", Stderr: "segfault", ExitCode: 139},
			wantSt: StatusRuntimeError,
		},
		{
			name:   "seccomp kill",
			sr:     &sandbox.RunResult{Status: "seccomp", Stderr: "bad syscall"},
			wantSt: StatusRuntimeError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := sandboxResultToJudgeResult(tt.sr)
			if r.Status != tt.wantSt {
				t.Errorf("status = %q, want %q", r.Status, tt.wantSt)
			}
		})
	}
}

func TestResultTypes(t *testing.T) {
	r := Result{
		Status:     StatusAccepted,
		TimeUsed:   42,
		MemoryUsed: 1024,
		Output:     "hello",
	}
	if r.Status != "Accepted" {
		t.Error("status mismatch")
	}
	if r.TimeUsed != 42 {
		t.Error("time mismatch")
	}
}

func TestRunUnsupportedLanguage(t *testing.T) {
	r := Run("brainfuck", "+", "", 1000, 128)
	if r.Status != StatusRuntimeError {
		t.Errorf("expected RE for unsupported language, got %q", r.Status)
	}
	if r.ErrorMsg == "" {
		t.Error("expected error message")
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusAccepted != "Accepted" {
		t.Error("StatusAccepted mismatch")
	}
	if StatusWrongAnswer != "Wrong Answer" {
		t.Error("StatusWrongAnswer mismatch")
	}
}
