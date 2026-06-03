package sandbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ============================================================================
// Seccomp BPF 系统调用白名单
// ============================================================================
//
// Seccomp（Secure Computing Mode）是 Linux 内核提供的一种安全机制，
// 允许进程限制自己能调用的系统调用。
//
// JudgeX 使用 seccomp 模式 2（SECCOMP_MODE_FILTER），即 BPF 过滤模式。
// 我们编译一个 BPF（Berkeley Packet Filter）程序，内核为每个系统调用
// 执行这个过滤程序：匹配则放行，不匹配则 SIGKILL。
//
// ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
// │ 用户代码 │ → │ 系统调用 │ → │ seccomp │ → │ 白名单  │ → ALLOW
// │ 请求打开 │   │ openat() │   │ BPF 过滤 │   │ 匹配？  │
// │ 文件    │   │          │   │          │ ─→ │ 不匹配  │ → KILL (SIGSYS)
// └──────────┘   └──────────┘   └──────────┘    └──────────┘
//
// 设计原则：最小权限原则
// - 只允许标准 C/C++/Go/Rust/Java/Python 运行所需的最少系统调用
// - 禁止危险调用：write(fd, buf, count) 只允许写到 stdout/stderr
//   （但我们没有参数级别的过滤，所以通过 chroot + 限制 fd 来配合）
// - 禁止网络相关：socket() 虽然被允许（某些运行时需要），但在 chroot + net ns
//   中被隔离，所以实际无法访问网络

// essentialSyscalls 是允许用户代码调用的系统调用白名单。
// 按功能分为以下几组：
var essentialSyscalls = []uint32{
	// ======== I/O 操作（文件读写、打开、关闭等） ========
	// 标准输入输出和安全文件操作
	unix.SYS_READ, unix.SYS_WRITE,
	// 打开文件（注意 open 和 openat 都允许，因为不同 libc 实现不同）
	unix.SYS_OPEN, unix.SYS_OPENAT,
	unix.SYS_CLOSE,
	// 获取文件信息（用于 stat、ls 等操作）
	unix.SYS_STAT, unix.SYS_FSTAT, unix.SYS_LSTAT, unix.SYS_NEWFSTATAT,
	// 随机读写（数据库和大文件处理用）
	unix.SYS_PREAD64, unix.SYS_PWRITE64,
	// 向量化 I/O（高性能读写）
	unix.SYS_READV, unix.SYS_WRITEV,
	// 文件指针定位
	unix.SYS_LSEEK,
	// 设备控制
	unix.SYS_IOCTL,
	// 文件权限检查
	unix.SYS_ACCESS,
	// 文件描述符复制（重定向 stdin/stdout/stderr）
	unix.SYS_DUP, unix.SYS_DUP2, unix.SYS_DUP3,
	// 管道创建（进程间通信）
	unix.SYS_PIPE, unix.SYS_PIPE2,
	// 文件控制（设置非阻塞、close-on-exec 等）
	unix.SYS_FCNTL,
	// 目录遍历
	unix.SYS_GETDENTS64,

	// ======== 内存管理 ========
	// 核心内存操作（几乎每个程序都需要）
	unix.SYS_MMAP,     // 分配内存映射
	unix.SYS_MPROTECT, // 修改内存保护属性（如 JIT 编译需要可执行内存）
	unix.SYS_MUNMAP,   // 释放内存映射
	unix.SYS_BRK,      // 堆内存分配（malloc 底层调用）
	unix.SYS_MREMAP,   // 重新映射
	// 内存优化提示
	unix.SYS_MADVISE, // 内存使用模式提示
	unix.SYS_MSYNC,   // 同步内存到磁盘
	unix.SYS_MINCORE, // 查询内存页是否在物理内存中

	// ======== 信号和进程控制 ========
	// 信号处理（所有运行时都需要）
	unix.SYS_RT_SIGACTION,   // 注册信号处理器
	unix.SYS_RT_SIGPROCMASK, // 屏蔽/解除屏蔽信号
	unix.SYS_RT_SIGRETURN,   // 从信号处理器返回
	// 进程退出
	unix.SYS_EXIT, unix.SYS_EXIT_GROUP,
	// 发送信号
	unix.SYS_KILL, unix.SYS_TKILL, unix.SYS_TGKILL,
	// 线程同步和睡眠
	unix.SYS_FUTEX,           // 用户空间互斥锁的核心（pthread_mutex 底层）
	unix.SYS_NANOSLEEP,       // 高精度睡眠
	unix.SYS_CLOCK_GETTIME,   // 获取时钟时间
	unix.SYS_GETTIMEOFDAY,    // 获取当前时间
	unix.SYS_SCHED_YIELD,     // 主动让出 CPU
	unix.SYS_CLOCK_NANOSLEEP, // 高精度时钟睡眠

	// ======== 进程身份信息 ========
	// 获取 PID、UID、GID 等（所有程序都需要）
	unix.SYS_GETPID, unix.SYS_GETTID,
	unix.SYS_GETUID, unix.SYS_GETGID,
	unix.SYS_GETEUID, unix.SYS_GETEGID,

	// ======== 线程管理 ========
	// 创建线程（clone vs fork：Go 运行时使用 clone）
	unix.SYS_CLONE,
	// 线程健壮性管理（NPTL 线程库需要）
	unix.SYS_SET_ROBUST_LIST, unix.SYS_SET_TID_ADDRESS,
	// CPU 架构和调度相关
	unix.SYS_ARCH_PRCTL,        // 架构特定操作（如 x86 的 FPU 控制）
	unix.SYS_PRCTL,             // 进程运行时控制
	unix.SYS_SCHED_GETAFFINITY, // 获取 CPU 亲和性

	// ======== C/C++ 运行时支持 ========
	unix.SYS_UNAME,      // 获取系统信息（uname，很多程序需要）
	unix.SYS_READLINK,   // 读取符号链接目标
	unix.SYS_READLINKAT, // 同上，但基于 dirfd
	unix.SYS_GETCWD,     // 获取当前工作目录
	unix.SYS_GETRANDOM,  // 获取随机数（安全编程需要，如 ASLR）
	unix.SYS_RSEQ,       // 可重启序列（Linux 4.18+ 的锁优化）
	unix.SYS_SETSOCKOPT, // 设置 socket 选项（某些库会调用）
	unix.SYS_SOCKET,     // 创建 socket（允许但被 network namespace 隔离）

	// ======== 程序执行 ========
	// 注意：execve 用来在 seccomp 设置后执行用户程序
	unix.SYS_EXECVE, unix.SYS_EXECVEAT,

	// ======== 内存屏障 ========
	unix.SYS_MEMBARRIER, // 内存屏障（Go 运行时需要）

	// ======== ELF 加载支持 ========
	unix.SYS_MLOCK,   // 锁定内存页
	unix.SYS_MUNLOCK, // 解锁内存页

	// ======== 信号相关 ========
	unix.SYS_RT_SIGPENDING, unix.SYS_RT_SIGTIMEDWAIT,
	unix.SYS_SIGALTSTACK, // 备用信号栈

	// ======== 资源限制 ========
	unix.SYS_PRLIMIT64, // 设置/获取进程资源限制
	unix.SYS_GETRLIMIT,
}

// ============================================================================
// BPF 过滤器编译和安装
// ============================================================================
//
// JudgeX 的 seccomp BPF 过滤逻辑：
//
// 1. 检查架构是否为 x86_64（不是则直接 KILL）
// 2. 加载系统调用号
// 3. 遍历白名单：如果匹配则 ALLOW
// 4. 如果都不匹配：KILL
//
// BPF 伪代码示例：
//
//	seccomp_data.arch == AUDIT_ARCH_X86_64 ?
//	  → yes: 继续
//	  → no:  KILL (防止 32 位兼容调用绕过检查)
//	seccomp_data.nr == SYS_READ ?
//	  → yes: ALLOW
//	  → no:  继续检查下一个
//	seccomp_data.nr == SYS_WRITE ?
//	  → yes: ALLOW
//	  → no:  继续检查下一个
//	... (遍历所有白名单系统调用)
//	→ 默认: KILL

const (
	seccompLoadArch  = 0x20 // ld [4]  — 从 seccomp_data 偏移 4 加载架构
	seccompLoadSysNr = 0x20 // ld [0]  — 从 seccomp_data 偏移 0 加载系统调用号
)

// buildBPF 构建 seccomp BPF 过滤程序。
// BPF（Berkeley Packet Filter）是一套虚拟机指令集，原本用于网络包过滤，
// 后来被 seccomp 复用为系统调用过滤引擎。
//
// 每条 BPF 指令结构：
//   - Code: 操作码（加载、跳转、返回等）
//   - Jt/Jf: 条件跳转目标（真/假）
//   - K: 常量参数
//
// 指令序列：
//  1. BPF_LD|BPF_W|BPF_ABS, 4     → 加载 arch 字段
//  2. BPF_JMP|BPF_JEQ|BPF_K, AUDIT_ARCH_X86_64 → 检查是否为 x86_64
//  3. [条件 Jt=1] → 跳过一个 KILL 指令（继续）
//  4. [条件 Jf=0] → 执行 KILL（非 x86_64 直接杀死）
//  5. RET KILL_THREAD              → 杀死非 x86_64 架构
//  6. BPF_LD|BPF_W|BPF_ABS, 0     → 加载系统调用号
//     7-8. 对每个白名单项：JEQ + RET ALLOW
//  9. RET KILL_THREAD              → 默认杀死
func buildBPF() []unix.SockFilter {
	var filter []unix.SockFilter

	// 第一步：检查架构
	// 从 seccomp_data 结构体的 arch 字段（偏移 4 字节，4 字节宽）加载
	filter = append(filter, unix.SockFilter{
		Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS,
		K:    4,
	})
	// 如果架构 == AUDIT_ARCH_X86_64，跳过下一条指令（不 kill）
	// 否则执行下一条指令（KILL）
	filter = append(filter, unix.SockFilter{
		Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K,
		Jt:   1, // 相等 -> 跳 1 条指令（跳过 KILL）
		Jf:   0, // 不等 -> 跳 0 条指令（执行 KILL）
		K:    unix.AUDIT_ARCH_X86_64,
	})
	filter = append(filter, unix.SockFilter{
		Code: unix.BPF_RET | unix.BPF_K,
		K:    uint32(unix.SECCOMP_RET_KILL_THREAD),
	})

	// 第二步：加载系统调用号
	// 从 seccomp_data->nr 字段（偏移 0，4 字节宽）加载
	filter = append(filter, unix.SockFilter{
		Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS,
		K:    0,
	})

	// 第三步：为每个白名单系统调用生成一对 BPF 指令
	//   JEQ syscall_number  → 匹配跳 0 条（ALLOW），不匹配跳 1 条（继续检查）
	//   RET ALLOW           → 放行
	for _, sc := range essentialSyscalls {
		filter = append(filter, unix.SockFilter{
			Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K,
			Jt:   0, // 匹配 -> 跳 0 条（执行下一条 RET ALLOW）
			Jf:   1, // 不匹配 -> 跳 1 条（跳过 ALLOW，继续检查下一个）
			K:    sc,
		})
		filter = append(filter, unix.SockFilter{
			Code: unix.BPF_RET | unix.BPF_K,
			K:    uint32(unix.SECCOMP_RET_ALLOW),
		})
	}

	// 第四步：默认规则——杀死所有不在白名单中的系统调用
	filter = append(filter, unix.SockFilter{
		Code: unix.BPF_RET | unix.BPF_K,
		K:    uint32(unix.SECCOMP_RET_KILL_THREAD),
	})

	return filter
}

// applySeccomp 编译 BPF 过滤器并将其安装到当前进程中。
// 必须在 syscall.Exec 执行用户代码之前调用。
//
// 安装步骤：
//
//	1. PR_SET_NO_NEW_PRIVS：禁止获取新的权限（阻止通过 setuid 程序提权）
//	   这个设置是 seccomp 的安全前提——确保子进程不能绕过限制
//	2. PR_SET_SECCOMP：设置 seccomp 过滤器

// 一旦 seccomp 过滤器安装成功：
//   - 后续所有系统调用都经过 BPF 过滤
//   - 白名单调用 → 正常执行
//   - 非白名单调用 → 进程收到 SIGSYS 信号并终止
//   - 过滤器对所有子进程自动继承（因为 NO_NEW_PRIVS 已设置）
//
// 安全警告：
//   - seccomp 过滤器是不可逆的（一旦设置不能移除，只能通过 exec 替换为新程序）
//   - 如果白名单过于严格，用户代码可能无法正常运行
//   - 如果白名单过于宽松，恶意代码可能利用允许的系统调用造成破坏
//     JudgeX 的白名单经过了 C/C++/Python/Java/Go/Rust 的全面测试
func applySeccomp() error {
	// 编译 BPF 过滤器程序
	filter := buildBPF()

	// 创建 seccomp BPF 程序结构体
	prog := &unix.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}

	// 步骤 1：设置 NO_NEW_PRIVS
	// 这确保即使 /tmp/judgex-*/main 有 setuid 位，exec 时也不会提升权限
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl NO_NEW_PRIVS: %w", err)
	}

	// 步骤 2：设置 seccomp 过滤器
	// 使用 SECCOMP_MODE_FILTER（模式 2），通过 BPF 程序自定义过滤逻辑
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(prog)), 0, 0); err != nil {
		return fmt.Errorf("seccomp filter: %w", err)
	}
	return nil
}
