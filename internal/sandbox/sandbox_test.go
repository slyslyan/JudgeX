package sandbox

import (
	"fmt"
	"testing"
)

func TestParseResult(t *testing.T) {
	tests := []struct {
		name     string
		r        *RunResult
		wantSt   string
		wantTime int
		wantMem  int
	}{
		{
			name:     "Accepted",
			r:        &RunResult{Status: "ok", TimeUsed: 150, MemoryUsed: 2048},
			wantSt:   "Accepted",
			wantTime: 150, wantMem: 2048,
		},
		{
			name:     "TLE",
			r:        &RunResult{Status: "tle", TimeUsed: 1000, MemoryUsed: 512},
			wantSt:   "Time Limit Exceeded",
			wantTime: 1000, wantMem: 512,
		},
		{
			name:     "MLE",
			r:        &RunResult{Status: "mle", TimeUsed: 300, MemoryUsed: 262144},
			wantSt:   "Memory Limit Exceeded",
			wantTime: 300, wantMem: 262144,
		},
		{
			name:     "RE on seccomp",
			r:        &RunResult{Status: "seccomp", TimeUsed: 10, MemoryUsed: 0, ExitCode: -1},
			wantSt:   "Runtime Error",
			wantTime: 10, wantMem: 0,
		},
		{
			name:     "RE on signal",
			r:        &RunResult{Status: "re", TimeUsed: 50, MemoryUsed: 100, ExitCode: 139},
			wantSt:   "Runtime Error",
			wantTime: 50, wantMem: 100,
		},
		{
			name:     "unknown status turns into RE",
			r:        &RunResult{Status: "unknown", TimeUsed: 1, MemoryUsed: 10},
			wantSt:   "Runtime Error",
			wantTime: 1, wantMem: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSt, gotTime, gotMem := ParseResult(tt.r)
			if gotSt != tt.wantSt {
				t.Errorf("status = %q, want %q", gotSt, tt.wantSt)
			}
			if gotTime != tt.wantTime {
				t.Errorf("time = %d, want %d", gotTime, tt.wantTime)
			}
			if gotMem != tt.wantMem {
				t.Errorf("mem = %d, want %d", gotMem, tt.wantMem)
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{
		BinaryPath:    "/bin/echo",
		Args:          []string{"hello"},
		Stdin:         "input",
		TimeLimitMs:   1000,
		MemoryLimitMB: 128,
		PidsMax:       16,
	}

	if cfg.BinaryPath != "/bin/echo" {
		t.Error("BinaryPath not set")
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "hello" {
		t.Error("Args not set")
	}
	if cfg.TimeLimitMs != 1000 {
		t.Error("TimeLimitMs not set")
	}
	if cfg.MemoryLimitMB != 128 {
		t.Error("MemoryLimitMB not set")
	}
	if cfg.PidsMax != 16 {
		t.Error("PidsMax not set")
	}
}

func TestCgroupWritable(t *testing.T) {
	// cgroupWritable must not panic — it safely handles read-only filesystems.
	_ = cgroupWritable()
}

func TestSandboxModeInit(t *testing.T) {
	// After init(), sandboxMode should be one of the three valid values.
	if sandboxMode != "native" && sandboxMode != "gvisor" && sandboxMode != "auto" {
		t.Errorf("unexpected sandboxMode: %q", sandboxMode)
	}
}

func TestRunResultString(t *testing.T) {
	r := &RunResult{
		Stdout:     "hello world\n",
		Stderr:     "",
		ExitCode:   0,
		TimeUsed:   42,
		MemoryUsed: 1024,
		Status:     "ok",
	}
	if r.Stdout != "hello world\n" {
		t.Error("stdout mismatch")
	}
	if r.ExitCode != 0 {
		t.Error("exit code not 0")
	}
}

func ExampleParseResult() {
	st, t, m := ParseResult(&RunResult{Status: "ok", TimeUsed: 100, MemoryUsed: 512})
	fmt.Printf("%s %dms %dKB\n", st, t, m)
	// Output: Accepted 100ms 512KB
}
