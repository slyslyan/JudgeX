package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ============================================================================
// 沙箱核心类型定义
// ============================================================================

// Config 是沙箱运行的配置参数。
// 每个测试用例对应一个 Config 实例，由 judge 包中的各语言处理函数构造。
//
// 核心字段说明：
//   - BinaryPath: 要执行的程序路径（编译产物的绝对路径，或解释器路径如 /usr/bin/python3）
//   - Args: 传递给程序的命令行参数（如 Python 的 ["-c", code]，Java 的 ["-cp", workDir, "Main"]）
//   - Stdin: 测试用例的输入数据，程序从标准输入读取
//   - TimeLimitMs: CPU 时间限制（毫秒），超时后沙箱会发送 SIGKILL
//   - MemoryLimitMB: 内存限制（MB），超限后 cgroup OOM Killer 会终止进程
//   - PidsMax: 允许的最大进程/线程数（防止 fork bomb）
//   - C/C++/Go/Rust：单进程程序，设 16 足够
//   - Java：JVM 有 GC 线程、编译器线程等，设 32
//   - Python：可能有多线程库，设 16-32
type Config struct {
	BinaryPath    string
	Args          []string
	Stdin         string
	TimeLimitMs   int
	MemoryLimitMB int
	PidsMax       int
}

// RunResult 是沙箱执行的结果，包含输出和资源使用情况。
// 由 Run() 函数返回，然后由 judge 包的 sandboxResultToJudgeResult() 转换为判题结果。
//
// 各字段说明：
//   - Stdout: 程序的标准输出（用于与预期输出比较判题）
//   - Stderr: 程序的标准错误（用于调试或错误报告）
//   - ExitCode: 退出码（正常退出为 0，运行时错误为非零）
//   - TimeUsed: 实际运行时间（毫秒）
//   - MemoryUsed: 峰值内存使用（KB，从 cgroup memory.peak 读取）
//   - Status: 沙箱内部状态码（"ok"=正常, "tle"=超时, "re"=运行时错误, "seccomp"=违规调用）
type RunResult struct {
	Stdout     string
	Stderr     string
	ExitCode   int
	TimeUsed   int64
	MemoryUsed int64
	Status     string
}

// ============================================================================
// 全局状态与初始化
// ============================================================================

// cgroupBase 是 cgroup v2 的根路径，在 init() 中确定。
// 通常是 /sys/fs/cgroup/judgex/，如果创建失败则回退到 /sys/fs/cgroup。
var cgroupBase string

// sandboxMode 是沙箱的运行模式，通过环境变量 SANDBOX_MODE 控制。
// "native": 完整隔离（cgroup + namespace + chroot + seccomp），需要 root 权限和宿主机
// "gvisor": 轻量隔离（chroot + seccomp，无 mount/unshare），适用于容器环境（K3s/gVisor）
// "auto": 自动检测 /sys/fs/cgroup 是否可写来决定
var sandboxMode string // "native", "gvisor", or "auto"

func init() {
	// 从环境变量读取模式
	sandboxMode = os.Getenv("SANDBOX_MODE")
	if sandboxMode == "" {
		sandboxMode = "auto"
	}

	// 自动检测模式：如果 cgroup 不可写，说明运行在 gVisor/Kata 容器中
	// 此时无法创建 cgroup 或使用 mount 系统调用，因此回退到 gvisor 模式
	if sandboxMode == "auto" {
		if !cgroupWritable() {
			sandboxMode = "gvisor"
		} else {
			sandboxMode = "native"
		}
	}

	// 在 native 模式下，确保 cgroup 基础目录存在
	// 如果 /sys/fs/cgroup/judgex/ 不存在，尝试创建并启用 CPU/memory/PIDs 控制器
	if sandboxMode == "native" {
		if fi, err := os.Stat("/sys/fs/cgroup/judgex"); err == nil && fi.IsDir() {
			cgroupBase = "/sys/fs/cgroup/judgex"
		} else {
			// 尝试创建 cgroup 目录并启用子树控制器
			// subtree_control 是 cgroup v2 的核心概念——父级必须先授权子控制器，
			// 子 cgroup 才能使用 CPU/memory/PIDs 限制
			if err := os.Mkdir("/sys/fs/cgroup/judgex", 0755); err == nil {
				os.WriteFile("/sys/fs/cgroup/judgex/cgroup.subtree_control",
					[]byte("+cpu +memory +pids"), 0644)
				cgroupBase = "/sys/fs/cgroup/judgex"
			} else {
				// 最后的回退：直接使用 cgroup 根（只能全局限制，不推荐）
				cgroupBase = "/sys/fs/cgroup"
			}
		}
	}
}

// Mode 返回当前沙箱模式，供 diagnostics 包获取状态
func Mode() string {
	return sandboxMode
}

// cgroupWritable 检测 /sys/fs/cgroup 是否可写。
// 在 gVisor/Kata 容器中，cgroup 文件系统通常是只读的或不存在的。
// 通过尝试创建一个临时文件来检测。
func cgroupWritable() bool {
	testFile := "/sys/fs/cgroup/.judgex-write-test"
	f, err := os.Create(testFile)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(testFile)
	return true
}

// unshareAvailable 检测系统是否支持 unshare 创建用户命名空间。
// 用户命名空间（user namespace）允许非 root 用户映射为容器内的 root，
// 是实现 mount namespace 等隔离的前提条件。
//
// 重要：K3s 容器默认禁止 user namespace（AppArmor 限制），
// 所以此函数会检测失败，然后回退到 fallback 模式。
//
// JUDGEX_NAMESPACE 环境变量可强制启用/禁用：
//
//	JUDGEX_NAMESPACE=1  -> 强制启用（即使检测失败）
//	JUDGEX_NAMESPACE=0  -> 强制禁用
func unshareAvailable() bool {
	if v := os.Getenv("JUDGEX_NAMESPACE"); v == "1" {
		return true
	} else if v == "0" {
		return false
	}
	// 实际检测：尝试运行 "unshare -U -r -- /bin/true"
	// -U: 创建新的 user namespace
	// -r: 把外层用户映射为内层 root
	cmd := exec.Command("/usr/bin/unshare", "-U", "-r", "--", "/bin/true")
	return cmd.Run() == nil
}

// ============================================================================
// 核心入口：Run
// ============================================================================

// Run 是沙箱的核心入口函数，接收配置后：
// 1. 在 native 模式下创建 cgroup（资源限制）
// 2. 根据模式选择不同的执行策略
// 3. 执行完成后从 cgroup 读取内存峰值
// 4. 分析退出状态（超时/信号/正常退出）
//
// ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
// │  Config   │ → │ Cgroup   │ → │  执行    │ → │  读资源  │ → RunResult
// │          │    │ 创建     │   │ 编译产物 │   │  统计    │
// └──────────┘    └──────────┘    └──────────┘    └──────────┘
func Run(cfg Config) (*RunResult, error) {
	start := time.Now()
	var cgPath string

	// 只有 native 模式需要创建 cgroup 来进行资源限制
	// gVisor 模式下由 gVisor 内核负责资源隔离
	if sandboxMode == "native" {
		cgPath = createCgroup(cfg)
	}

	// 设置总超时 = 用户代码时间限制 + 5 秒缓冲
	// 这层超时是兜底措施——即使 cgroup 的 cpu.max 没生效（比如创建失败），
	// context.WithTimeout 也能通过杀死进程来避免无限循环
	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(cfg.TimeLimitMs+5000)*time.Millisecond)
	defer cancel()

	// 根据模式选择执行策略
	switch sandboxMode {
	case "gvisor":
		// gVisor 模式：用 /proc/self/exe（当前进程）重新执行
		// 通过环境变量 JUDGEX_USE_JAIL=1 让 SandboxInit() 走 chroot + seccomp 路径
		cmd = buildGVisorCmd(ctx, cfg)
	case "native":
		if unshareAvailable() {
			// 完整隔离：使用 unshare 命名空间隔离
			cmd = buildNamespaceCmd(ctx, cfg, cgPath)
		} else {
			// 容器内回退：用 /proc/self/exe 但不创建 namespace
			cmd = buildFallbackCmd(ctx, cfg, cgPath)
		}
	default:
		cmd = buildFallbackCmd(ctx, cfg, cgPath)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	result := &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		TimeUsed: elapsed,
	}

	// 从 cgroup 读取内存峰值（memory.peak 是 cgroup v2 的特性）
	// 注意：即使进程被 OOM kill，memory.peak 依然能读到被杀前的峰值
	if cgPath != "" {
		if data, err := os.ReadFile(cgPath + "/memory.peak"); err == nil {
			if peak, perr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); perr == nil {
				result.MemoryUsed = peak / 1024 // 字节 → KB
			}
		}
		// 清理 cgroup 目录（删除后内核自动回收）
		os.Remove(cgPath)
	}

	// 错误分析：将操作系统退出状态映射为沙箱内部状态码
	if err != nil {
		// 1. 超时：context deadline exceeded
		if ctx.Err() == context.DeadlineExceeded {
			result.Status = "tle"
			return result, nil
		}
		// 2. 非零退出：检查是否被信号终止
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					sig := status.Signal()
					switch sig {
					case syscall.SIGKILL, syscall.SIGXCPU:
						// SIGKILL: cgroup OOM killer 或 cgroup cpu.max 超限
						// SIGXCPU: RLIMIT_CPU 超限（备用机制）
						result.Status = "tle"
					case syscall.SIGSYS:
						// SIGSYS: 非法系统调用（seccomp 拦截）
						result.Status = "seccomp"
					default:
						// 其他信号（如 SIGSEGV、SIGFPE 等）-> 运行时错误
						result.Status = "re"
						result.ExitCode = -int(sig)
					}
				} else {
					// 正常非零退出（如 C++ 的 return 1）
					result.ExitCode = status.ExitStatus()
					result.Status = "re"
				}
			}
			return result, nil
		}
		// 3. 其他错误（如找不到二进制文件）
		result.Status = "re"
		return result, nil
	}

	// 正常退出
	result.Status = "ok"
	return result, nil
}

// ============================================================================
// 执行策略：三种模式
// ============================================================================

// buildNamespaceCmd 构建使用完整 unshare 命名空间隔离的命令。
// 这是最严格的隔离级别，通过 Linux 命名空间实现：
//
//	-U: user namespace（容器内 UID 0，容器外普通用户）
//	-r: 映射为 root
//	-p: PID namespace（容器内看不到外部进程）
//	-m: mount namespace（独立的挂载点，用于 chroot）
//	-n: net namespace（独立的网络栈，防止网络访问）
//	-i: IPC namespace（独立的进程间通信）
//
// 注意：不使用 --mount-proc 标志，因为需要先执行 mount --make-private /，
// 这需要 CAP_SYS_ADMIN。替代方案是在 setupChrootJail() 中通过 bind mount
// 创建隔离的 /proc。
//
// 执行方式：通过 /proc/self/exe 重新执行当前进程
// （即 judge-worker 或 server 二进制自身），
// 参数为 "judgex-sandbox-init <binary> [args...]"
func buildNamespaceCmd(ctx context.Context, cfg Config, cgPath string) *exec.Cmd {
	innerArgs := append([]string{"judgex-sandbox-init", cfg.BinaryPath}, cfg.Args...)
	unshareArgs := []string{
		"-U", "-r", "-p", "-m", "-n", "-i",
		"--fork", "--",
		"/proc/self/exe",
	}
	unshareArgs = append(unshareArgs, innerArgs...)
	cmd := exec.CommandContext(ctx, "/usr/bin/unshare", unshareArgs...)
	cmd.Stdin = strings.NewReader(cfg.Stdin)
	cmd.Env = append(os.Environ(),
		"JUDGEX_CGROUP_PATH="+cgPath,
		"JUDGEX_USE_JAIL=1",
		"PATH=/usr/bin:/bin:/usr/local/bin",
	)
	return cmd
}

// buildFallbackCmd 构建无命名空间隔离的回退命令。
// 当 unshare 不可用时（如 K3s 容器中），使用此模式。
// 仍然通过 /proc/self/exe 重新执行，走 chroot + seccomp 路径，
// 但没有 user namespace 和 mount namespace 隔离。
//
// 此模式的安全性低于完整隔离：
//   - 有 chroot（文件系统隔离）
//   - 有 seccomp（系统调用过滤）
//   - 有 cgroup（资源限制）
//   - 无 namespace 隔离（进程可见，网络共享）
func buildFallbackCmd(ctx context.Context, cfg Config, cgPath string) *exec.Cmd {
	innerArgs := append([]string{"judgex-sandbox-init", cfg.BinaryPath}, cfg.Args...)
	cmd := exec.CommandContext(ctx, "/proc/self/exe", innerArgs...)
	cmd.Stdin = strings.NewReader(cfg.Stdin)
	cmd.Env = append(os.Environ(),
		"JUDGEX_CGROUP_PATH="+cgPath,
		"PATH=/usr/bin:/bin:/usr/local/bin",
	)
	return cmd
}

// buildGVisorCmd 构建 gVisor 模式下的执行命令。
// gVisor 提供了内核级别的隔离，所以我们不需要 unshare 命名空间。
// 但 gVisor 的 seccomp 限制更严格（mount 系统调用被 sentry 拦截），
// 所以需要使用 setupChrootJailSimple（不依赖 mount 系统调用的简化版 chroot）。
//
// 注意：gVisor 模式下不传 JUDGEX_CGROUP_PATH，因为 cgroup 不可写。
// 资源限制由 gVisor 的沙箱配置管理。
func buildGVisorCmd(ctx context.Context, cfg Config) *exec.Cmd {
	innerArgs := append([]string{"judgex-sandbox-init", cfg.BinaryPath}, cfg.Args...)
	cmd := exec.CommandContext(ctx, "/proc/self/exe", innerArgs...)
	cmd.Stdin = strings.NewReader(cfg.Stdin)
	cmd.Env = append(os.Environ(),
		"JUDGEX_USE_JAIL=1",
		"JUDGEX_SANDBOX_MODE=gvisor",
		"PATH=/usr/bin:/bin:/usr/local/bin",
	)
	return cmd
}

// ============================================================================
// Cgroup 管理（仅 native 模式）
// ============================================================================

// createCgroup 在 cgroup v2 文件系统中创建子 cgroup 并设置资源限制。
//
// cgroup v2 层次结构：
//
//	/sys/fs/cgroup/
//	├── judgex/                    ← 根（由 init() 创建并配置 subtree_control）
//	│   ├── judgex-<pid>-<ts>/    ← 本次运行的子 cgroup（由此函数创建）
//	│   │   ├── cpu.max           ← CPU 时间限制
//	│   │   ├── memory.max        ← 硬内存限制
//	│   │   ├── memory.high       ← 软内存限制（触发回收但不会 OOM）
//	│   │   ├── pids.max          ← 最大进程数
//	│   │   └── memory.peak       ← 运行后读取内存峰值
//	│   │
//	│   └── ...（更多运行实例）
//
// cgroup 限制和 user namespace 的关系：
// 当进程在 user namespace 中时，往 cgroup.procs 写 PID 需要 uid/gid 映射一致。
// 这就是为什么我们在 SandboxInit() 中才加入 cgroup——此时进程已经在 namespace
// 内部且 uid/gid 已映射，写 cgroup.procs 会成功。
func createCgroup(cfg Config) string {
	if cgroupBase == "" {
		return ""
	}
	// 创建以进程 PID 和时间戳命名的子 cgroup，确保唯一性
	cgPath := fmt.Sprintf("%s/judgex-%d-%d", cgroupBase, os.Getpid(), time.Now().UnixNano())
	if err := os.Mkdir(cgPath, 0755); err != nil {
		return ""
	}

	// CPU 限制：格式为 "quota period"
	// quota = timeLimitMs * 1000（微秒），period = 100000 微秒（100ms）
	// 例如：2000ms 限制 -> "2000000 100000" -> 每 100ms 最多给 2000ms 的 CPU
	if cfg.TimeLimitMs > 0 {
		cpuMax := fmt.Sprintf("%d 100000", cfg.TimeLimitMs*1000)
		os.WriteFile(cgPath+"/cpu.max", []byte(cpuMax), 0644)
	}

	// 内存限制：
	//   memory.max = 硬限制（超过触发 OOM Kill）
	//   memory.high = 软限制（超过触发内存回收，但不一定 kill）
	// 同时设置 max 和 high 是为了让内存回收尽早介入
	if cfg.MemoryLimitMB > 0 {
		bytesVal := strconv.FormatInt(int64(cfg.MemoryLimitMB)*1024*1024, 10)
		os.WriteFile(cgPath+"/memory.max", []byte(bytesVal), 0644)
		os.WriteFile(cgPath+"/memory.high", []byte(bytesVal), 0644)
	}

	// 进程数限制：防止 fork bomb 攻击
	if cfg.PidsMax > 0 {
		os.WriteFile(cgPath+"/pids.max", []byte(strconv.Itoa(cfg.PidsMax)), 0644)
	}

	return cgPath
}

// ============================================================================
// Chroot 隔离：两种实现
// ============================================================================
//
// JudgeX 的 chroot 隔离有两个版本：
//
// 1. setupChrootJail (native 模式)：
//    - 使用 syscall.Mount 创建 bind mount（需要 mount namespace + CAP_SYS_ADMIN）
//    - 把 /usr、/lib、/bin、/etc 以只读方式挂载到 jail 目录
//    - 在 /tmp 上挂载 tmpfs（可写但 128MB 限制）
//    - 挂载隔离的 /proc
//    - 通过 mknod 创建 /dev/null、/dev/zero、/dev/urandom
//
// 2. setupChrootJailSimple (gVisor 模式)：
//    - 不使用任何 mount 系统调用（gVisor 禁止 mount）
//    - 直接将依赖的动态库文件复制到 jail 目录
//    - 通过 mknod 创建 /dev 节点（mknod 在 gVisor 中允许）
//    - 适用于 Alpine Linux（musl libc）

// setupChrootJailSimple 创建最小化的 chroot 环境，不使用 mount 系统调用。
// 用于 gVisor 模式下——gVisor 的 sentry 拦截 mount 系统调用，
// 所以只能通过文件复制来构造隔离环境。
//
// 实现方式：
// 1. 在 /tmp 下创建临时目录
// 2. 创建必要的子目录（tmp, dev, lib, usr/lib, usr/bin, bin, etc）
// 3. 通过 mknod 创建 /dev/null、/dev/zero、/dev/urandom
// 4. 复制目标二进制文件到 jail 内
// 5. 复制 musl/Alpine 的动态库依赖（静态链接的 C++ 不需要，但 Python/Java 需要）
// 6. 复制 /etc/resolv.conf 和 /etc/hosts
func setupChrootJailSimple(targetBinary string) (jailDir string, jailBinary string, err error) {
	jailDir, err = os.MkdirTemp("/tmp", "jail-")
	if err != nil {
		return "", "", fmt.Errorf("mkdirtemp: %w", err)
	}

	cleanup := func() {
		os.RemoveAll(jailDir)
	}

	// 创建最小化的目录结构
	for _, d := range []string{"tmp", "dev", "lib", "usr/lib", "usr/bin", "bin", "etc"} {
		if err := os.MkdirAll(filepath.Join(jailDir, d), 0755); err != nil {
			cleanup()
			return "", "", fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// 创建设备节点（mknod 在 gVisor 中被允许，是少数例外之一）
	devDir := filepath.Join(jailDir, "dev")
	unix.Mknod(filepath.Join(devDir, "null"), 0666|syscall.S_IFCHR, int(unix.Mkdev(1, 3)))
	unix.Mknod(filepath.Join(devDir, "zero"), 0666|syscall.S_IFCHR, int(unix.Mkdev(1, 5)))
	unix.Mknod(filepath.Join(devDir, "urandom"), 0444|syscall.S_IFCHR, int(unix.Mkdev(1, 9)))

	// 将目标二进制复制到 jail 内
	copyBinaryToJail := func(srcPath string) string {
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return ""
		}
		destPath := filepath.Join(jailDir, srcPath)
		os.MkdirAll(filepath.Dir(destPath), 0755)
		os.WriteFile(destPath, data, 0755)
		return srcPath
	}

	jb := copyBinaryToJail(targetBinary)
	if jb == "" {
		// 如果目标二进制不在标准路径下（如编译器编译的临时文件），
		// 复制到 jail 的 /tmp/ 下
		data, err := os.ReadFile(targetBinary)
		if err != nil {
			cleanup()
			return "", "", fmt.Errorf("read binary %s: %w", targetBinary, err)
		}
		destName := filepath.Base(targetBinary)
		destPath := filepath.Join(jailDir, "tmp", destName)
		if err := os.WriteFile(destPath, data, 0755); err != nil {
			cleanup()
			return "", "", fmt.Errorf("copy binary: %w", err)
		}
		jailBinary = "/tmp/" + destName
	} else {
		jailBinary = jb
	}

	// 复制 musl/Alpine 的动态库依赖
	// 注意：C++ 代码编译时用了 -static 标志所以通常不需要动态库，
	// 但 Python 解释器和 Java JVM 需要动态链接
	essentialLibs := []string{
		"/lib/ld-musl-x86_64.so.1",
		"/usr/lib/libstdc++.so.6",
		"/usr/lib/libgcc_s.so.1",
	}
	for _, lib := range essentialLibs {
		if data, err := os.ReadFile(lib); err == nil {
			destLib := filepath.Join(jailDir, lib)
			os.MkdirAll(filepath.Dir(destLib), 0755)
			os.WriteFile(destLib, data, 0755)
		}
	}

	// 复制 /etc 配置文件（部分程序启动时需要读取）
	for _, f := range []string{"/etc/resolv.conf", "/etc/hosts"} {
		if data, err := os.ReadFile(f); err == nil {
			os.WriteFile(filepath.Join(jailDir, f), data, 0644)
		}
	}

	return jailDir, jailBinary, nil
}

// setupChrootJail 创建完整的 chroot 隔离环境（native 模式）。
// 通过 mount 系统调用将系统目录以只读方式绑定挂载到 jail 目录中。
//
// 完整的挂载结构：
//
//	/tmp/jail-xxx/          ← tmpfs（内存文件系统）
//	├── usr/  → rbind /usr  ← 只读
//	├── lib/  → rbind /lib  ← 只读（如果存在）
//	├── lib64/ → rbind /lib64 ← 只读（如果存在）
//	├── bin/  → rbind /bin  ← 只读
//	├── etc/  → rbind /etc  ← 只读
//	├── proc/ → proc        ← 新的 proc 实例
//	├── dev/
//	│   ├── null             ← mknod
//	│   ├── zero             ← mknod
//	│   └── urandom          ← mknod
//	└── tmp/  → tmpfs        ← 可写，128MB 限制
//
// 为什么使用 bind mount：
//   - 比复制文件更高效（不占用额外磁盘空间）
//   - 保持硬链接和文件属性
//   - 内核维护的 /proc 和 /dev 文件系统工作正常
//
// 为什么需要 mount namespace：
//   - bind mount 会影响主机挂载表
//   - 在 mount namespace 中操作，外面的主机不受影响
func setupChrootJail(targetBinary string) (jailDir string, jailBinary string, err error) {
	jailDir, err = os.MkdirTemp("/tmp", "jail-")
	if err != nil {
		return "", "", fmt.Errorf("mkdirtemp: %w", err)
	}

	// 在 jail 目录上挂载 tmpfs（内存文件系统，重启即失）
	// 这样所有 bind mount 都只影响内存中的挂载表，不影响磁盘
	if err := syscall.Mount("tmpfs", jailDir, "tmpfs", 0, ""); err != nil {
		os.RemoveAll(jailDir)
		return "", "", fmt.Errorf("mount tmpfs: %w", err)
	}

	// cleanup 函数：卸载所有子挂载点后 unmount tmpfs 并删除目录
	// 注意卸载顺序很重要：必须先卸载子挂载点，再卸载 tmpfs 本身
	cleanup := func() {
		syscall.Unmount(filepath.Join(jailDir, "proc"), 0)
		syscall.Unmount(filepath.Join(jailDir, "tmp"), 0)
		syscall.Unmount(filepath.Join(jailDir, "usr"), 0)
		syscall.Unmount(filepath.Join(jailDir, "lib"), 0)
		syscall.Unmount(filepath.Join(jailDir, "lib64"), 0)
		syscall.Unmount(filepath.Join(jailDir, "bin"), 0)
		syscall.Unmount(filepath.Join(jailDir, "etc"), 0)
		syscall.Unmount(jailDir, 0)
		os.RemoveAll(jailDir)
	}

	// 创建必要的目录结构
	dirs := []string{"usr", "lib", "lib64", "bin", "etc", "proc", "dev", "tmp"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(jailDir, d), 0755); err != nil {
			cleanup()
			return "", "", fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// 以下 bind mount 操作必须在 mount namespace 中进行
	// 否则会影响主机的挂载表（K3s 容器中 mount namespace 可能不可用）

	// 绑定挂载 /usr（递归，包括其子挂载点）
	if err := syscall.Mount("/usr", filepath.Join(jailDir, "usr"), "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		cleanup()
		return "", "", fmt.Errorf("bind /usr: %w", err)
	}
	// 重新挂载为只读（bind mount 默认可写，需要 remount 加 MS_RDONLY）
	if err := syscall.Mount("", filepath.Join(jailDir, "usr"), "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, ""); err != nil {
		cleanup()
		return "", "", fmt.Errorf("remount /usr ro: %w", err)
	}

	// 同样方式挂载 /lib、/lib64、/bin、/etc
	if _, err := os.Stat("/lib"); err == nil {
		if err := syscall.Mount("/lib", filepath.Join(jailDir, "lib"), "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			cleanup()
			return "", "", fmt.Errorf("bind /lib: %w", err)
		}
		syscall.Mount("", filepath.Join(jailDir, "lib"), "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, "")
	}

	if _, err := os.Stat("/lib64"); err == nil {
		if err := syscall.Mount("/lib64", filepath.Join(jailDir, "lib64"), "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			cleanup()
			return "", "", fmt.Errorf("bind /lib64: %w", err)
		}
		syscall.Mount("", filepath.Join(jailDir, "lib64"), "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, "")
	}

	if err := syscall.Mount("/bin", filepath.Join(jailDir, "bin"), "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		cleanup()
		return "", "", fmt.Errorf("bind /bin: %w", err)
	}
	syscall.Mount("", filepath.Join(jailDir, "bin"), "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, "")

	if err := syscall.Mount("/etc", filepath.Join(jailDir, "etc"), "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		cleanup()
		return "", "", fmt.Errorf("bind /etc: %w", err)
	}
	syscall.Mount("", filepath.Join(jailDir, "etc"), "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, "")

	// 挂载隔离的 /proc 文件系统
	// 注意：这里不是 bind mount 主机的 /proc，而是挂载一个新的 proc 实例
	// 这样 jail 中只能看到自己的进程，看不到主机进程
	if err := syscall.Mount("proc", filepath.Join(jailDir, "proc"), "proc", 0, ""); err != nil {
		cleanup()
		return "", "", fmt.Errorf("mount proc: %w", err)
	}

	// 在 /dev 下创建设备节点
	devDir := filepath.Join(jailDir, "dev")
	if err := unix.Mknod(filepath.Join(devDir, "null"), 0666|syscall.S_IFCHR, int(unix.Mkdev(1, 3))); err != nil {
		cleanup()
		return "", "", fmt.Errorf("mknod null: %w", err)
	}
	if err := unix.Mknod(filepath.Join(devDir, "zero"), 0666|syscall.S_IFCHR, int(unix.Mkdev(1, 5))); err != nil {
		cleanup()
		return "", "", fmt.Errorf("mknod zero: %w", err)
	}
	if err := unix.Mknod(filepath.Join(devDir, "urandom"), 0444|syscall.S_IFCHR, int(unix.Mkdev(1, 9))); err != nil {
		cleanup()
		return "", "", fmt.Errorf("mknod urandom: %w", err)
	}

	// 在 /tmp 挂载 tmpfs，限制大小为 128MB
	// 这样用户代码不能写满磁盘（只能在内存中写最多 128MB）
	if err := syscall.Mount("tmpfs", filepath.Join(jailDir, "tmp"), "tmpfs", 0, "size=128M"); err != nil {
		cleanup()
		return "", "", fmt.Errorf("mount /tmp: %w", err)
	}

	// 复制目标二进制到 jail 内
	// 如果二进制在标准路径下（/usr/bin/python3），直接 bind mount 就包含了
	// 如果是临时编译产物（如 /tmp/judgex-*/main），需要复制到 /tmp 下
	jailBinary = targetBinary
	if !strings.HasPrefix(targetBinary, "/usr/") && !strings.HasPrefix(targetBinary, "/bin/") {
		src, err := os.ReadFile(targetBinary)
		if err != nil {
			cleanup()
			return "", "", fmt.Errorf("read binary %s: %w", targetBinary, err)
		}
		destName := filepath.Base(targetBinary)
		destPath := filepath.Join(jailDir, "tmp", destName)
		if err := os.WriteFile(destPath, src, 0755); err != nil {
			cleanup()
			return "", "", fmt.Errorf("copy binary: %w", err)
		}
		jailBinary = "/tmp/" + destName
	}

	return jailDir, jailBinary, nil
}

// ============================================================================
// Reexec 入口：SandboxInit
// ============================================================================

// SandboxInit 是沙箱的 reexec 入口点。
// 设计思路：Go 程序的 /proc/self/exe 指向自身二进制，我们可以用不同的参数
// 重新执行自己。通过检测第一个参数是否为 "judgex-sandbox-init" 来判断
// 是否进入沙箱初始化流程。
//
// 执行流程：
//
//	┌─────────────┐     ┌────────────────┐     ┌──────────────┐     ┌─────────────┐
//	│  Run() 调用  │ →  │ SandboxInit()  │ →  │ Join Cgroup  │ →  │ Chroot Jail │
//	│ exec.Command │    │ (重新执行进程)  │    │ (写 cgroup   │    │ (文件系统    │
//	│              │    │ 参数: sandbox- │    │  .procs)     │    │  隔离)      │
//	│              │    │  init <binary> │    │              │    │             │
//	└─────────────┘     └────────────────┘    └──────────────┘    └──────┬──────┘
//	                                                                     ▼
//	                                                            ┌─────────────┐
//	                                                            │  seccomp    │
//	                                                            │  系统调用   │
//	                                                            │  过滤      │
//	                                                            └──────┬──────┘
//	                                                                   ▼
//	                                                            ┌─────────────┐
//	                                                            │  syscall.   │
//	                                                            │  Exec(目标) │
//	                                                            │  执行用户   │
//	                                                            │  代码      │
//	                                                            └─────────────┘
//
// 注意：此函数由 /proc/self/exe 以新进程方式调用（在 judge-worker 或 server
// 进程启动时也会被调一次，但那一次会因为参数不匹配而直接返回 false）
func SandboxInit() bool {
	// 检查是否是 sandbox reexec 入口
	if len(os.Args) < 3 || os.Args[1] != "judgex-sandbox-init" {
		return false
	}

	targetBinary := os.Args[2]
	targetArgs := os.Args[3:]

	// 第一步：加入 cgroup（把自己的 PID 写入 cgroup.procs）
	// 相当于把自己"关进"资源限制的笼子里
	// 注意：这一步必须在 chroot 之前做，因为 chroot 后 /sys 不可见
	if cgPath := os.Getenv("JUDGEX_CGROUP_PATH"); cgPath != "" {
		os.WriteFile(cgPath+"/cgroup.procs", []byte(strconv.Itoa(os.Getpid())), 0644)
	}

	jailBinary := targetBinary

	// 第二步：创建并进入 chroot jail
	if os.Getenv("JUDGEX_USE_JAIL") == "1" {
		var jailDir string
		var jb string
		var err error

		if os.Getenv("JUDGEX_SANDBOX_MODE") == "gvisor" {
			// gVisor 模式：使用无 mount 的简化版 chroot
			jailDir, jb, err = setupChrootJailSimple(targetBinary)
		} else {
			// native 模式（且有 mount namespace）：使用完整 chroot
			jailDir, jb, err = setupChrootJail(targetBinary)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "jail setup failed: %v\n", err)
			os.Exit(126)
		}
		jailBinary = jb

		// 执行 chroot
		// 先 cd 到 jail 目录，然后 chroot(".") 将当前目录作为新根
		if err := syscall.Chdir(jailDir); err != nil {
			fmt.Fprintf(os.Stderr, "chdir jail: %v\n", err)
			os.Exit(126)
		}
		if err := syscall.Chroot("."); err != nil {
			fmt.Fprintf(os.Stderr, "chroot: %v\n", err)
			os.Exit(126)
		}
		if err := syscall.Chdir("/"); err != nil {
			fmt.Fprintf(os.Stderr, "chdir /: %v\n", err)
			os.Exit(126)
		}
	}

	// 第三步：应用 seccomp BPF 系统调用过滤（在 exec 目标程序前锁定安全策略）
	// seccomp 必须在 exec 之前设置，否则子进程可能继承父进程的宽松限制
	if err := applySeccomp(); err != nil {
		fmt.Fprintf(os.Stderr, "seccomp: %v\n", err)
		os.Exit(126)
	}

	// 第四步：执行目标程序
	// syscall.Exec 用目标程序替换当前进程（不创建新进程），
	// 这样 seccomp 策略会持续生效，因为安全策略是附着在进程上的
	argv := append([]string{jailBinary}, targetArgs...)
	if err := syscall.Exec(jailBinary, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "exec %s: %v\n", jailBinary, err)
		os.Exit(127)
	}
	return true
}

// ============================================================================
// 结果转换
// ============================================================================

// ParseResult 将沙箱内部状态码转换为判题系统使用的状态字符串。
// 这是 sandbox 包和 judge 包之间的转换层。
//
// 映射关系：
//
//	sandbox 内部状态      → 判题状态
//	"ok"                 → "Accepted"
//	"tle"                → "Time Limit Exceeded"
//	"mle"                → "Memory Limit Exceeded"
//	其他（"re", "seccomp"）→ "Runtime Error"
//
// 注意：seccomp 违规（调用非白名单系统调用）也被归为 Runtime Error，
// 但 sandbox 包内部保留了 "seccomp" 状态，方便调试和日志记录。
func ParseResult(r *RunResult) (status string, timeUsed, memUsed int) {
	timeUsed = int(r.TimeUsed)
	memUsed = int(r.MemoryUsed)
	switch r.Status {
	case "ok":
		status = "Accepted"
	case "tle":
		status = "Time Limit Exceeded"
	case "mle":
		status = "Memory Limit Exceeded"
	default:
		status = "Runtime Error"
	}
	return status, timeUsed, memUsed
}
