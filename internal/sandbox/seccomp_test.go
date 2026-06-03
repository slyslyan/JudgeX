package sandbox

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestBuildBPF(t *testing.T) {
	filter := buildBPF()

	if len(filter) == 0 {
		t.Fatal("empty BPF filter")
	}

	// First instruction loads arch from offset 4
	if filter[0].Code != uint16(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS) {
		t.Error("first instruction should load arch")
	}
	if filter[0].K != 4 {
		t.Error("arch offset should be 4")
	}

	// Second instruction compares against x86_64 arch
	if filter[1].Code != uint16(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K) {
		t.Error("second instruction should be JEQ for arch check")
	}

	// Third instruction: kill if wrong arch
	if filter[2].Code != uint16(unix.BPF_RET|unix.BPF_K) {
		t.Error("third instruction should be RET (kill)")
	}
	if filter[2].K != uint32(unix.SECCOMP_RET_KILL_THREAD) {
		t.Error("wrong arch should kill thread")
	}

	// Every allowed syscall should have a JEQ + RET ALLOW pair
	allowedCount := 0
	for i := 0; i < len(filter); i++ {
		if filter[i].Code == uint16(unix.BPF_RET|unix.BPF_K) &&
			filter[i].K == uint32(unix.SECCOMP_RET_ALLOW) {
			allowedCount++
		}
	}
	if allowedCount < 40 {
		t.Errorf("too few allowed syscalls: %d (expected >= 40)", allowedCount)
	}

	// Last instruction must be RET KILL_THREAD (default: deny)
	last := filter[len(filter)-1]
	if last.Code != uint16(unix.BPF_RET|unix.BPF_K) {
		t.Error("last instruction should be RET")
	}
	if last.K != uint32(unix.SECCOMP_RET_KILL_THREAD) {
		t.Errorf("default action should be KILL_THREAD, got %d", last.K)
	}
}

func TestBuildBPFSyscallWhitelist(t *testing.T) {
	// Verify critical syscalls are in the whitelist.
	required := []uint32{
		unix.SYS_READ,
		unix.SYS_WRITE,
		unix.SYS_OPENAT,
		unix.SYS_CLOSE,
		unix.SYS_EXIT,
		unix.SYS_EXIT_GROUP,
		unix.SYS_EXECVE,
		unix.SYS_MMAP,
		unix.SYS_MPROTECT,
		unix.SYS_MUNMAP,
		unix.SYS_FUTEX,
		unix.SYS_CLONE,
		unix.SYS_IOCTL,
		unix.SYS_FCNTL,
		unix.SYS_STAT,
		unix.SYS_FSTAT,
	}

	whitelist := make(map[uint32]bool)
	for _, sc := range essentialSyscalls {
		whitelist[sc] = true
	}

	for _, sc := range required {
		if !whitelist[sc] {
			t.Errorf("critical syscall %d not in whitelist", sc)
		}
	}
}

func TestEssentialSyscallsNoDuplicates(t *testing.T) {
	seen := make(map[uint32]bool)
	for _, sc := range essentialSyscalls {
		if seen[sc] {
			t.Errorf("duplicate syscall %d in whitelist", sc)
		}
		seen[sc] = true
	}
}
