package judge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"judgex/internal/sandbox"
)

// ============================================================================
// 判题状态常量
// ============================================================================
const (
	StatusAccepted            = "Accepted"              // 通过：输出完全匹配
	StatusWrongAnswer         = "Wrong Answer"          // 答案错误：输出不匹配
	StatusTimeLimitExceeded   = "Time Limit Exceeded"   // 超时：运行超过题目时限
	StatusMemoryLimitExceeded = "Memory Limit Exceeded" // 超内存：运行超过内存限制
	StatusRuntimeError        = "Runtime Error"         // 运行时错误：崩溃/非零退出
	StatusCompileError        = "Compile Error"         // 编译错误：代码无法编译
)

// Result 是单个测试用例的判题结果
type Result struct {
	Status     string // 判题状态（Accepted/Wrong Answer/TLE/MLE/RE/CE）
	TimeUsed   int    // 运行耗时（毫秒）
	MemoryUsed int    // 内存使用（MB）
	Output     string // 程序实际输出
	ErrorMsg   string // 错误信息（编译错误输出或运行错误信息）
}

// Run 是判题引擎的主入口，接收语言、代码、输入数据和资源限制，返回判题结果。
// 流程：创建临时工作目录 → 根据语言调用对应的编译/运行函数 → 清理目录
//
// 判题并发安全：每次调用创建独立临时目录，互不干扰
func Run(language, code, input string, timeLimit, memoryLimit int) Result {
	// 创建临时工作目录，用于存放源代码和编译产物
	// 目录名前缀 "judgex-*" 方便调试时识别
	workDir, err := os.MkdirTemp("", "judgex-*")
	if err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: "failed to create work dir"}
	}
	defer os.RemoveAll(workDir) // 函数结束时清理，防止磁盘泄漏

	// 根据语言分发到不同的处理函数
	// 每种语言有自己的编译命令和沙箱配置（如 Java 的 PidsMax 更高，因为 JVM 多线程）
	switch language {
	case "cpp", "c":
		return runCpp(workDir, code, input, timeLimit, memoryLimit)
	case "python", "python3":
		return runPython(code, input, timeLimit, memoryLimit)
	case "java":
		return runJava(workDir, code, input, timeLimit, memoryLimit)
	case "go":
		return runGo(workDir, code, input, timeLimit, memoryLimit)
	case "rust":
		return runRust(workDir, code, input, timeLimit, memoryLimit)
	default:
		return Result{Status: StatusRuntimeError, ErrorMsg: "unsupported language: " + language}
	}
}

// ============================================================================
// 各语言判题实现
// 每种语言分两步：编译（如果需要）→ 在沙箱中运行
// 编译用 context.WithTimeout 防止恶意代码在编译阶段死循环
// ============================================================================

// runCpp 编译并运行 C++ 代码
// 编译参数: -O2 优化, -std=c++17 标准, -static 静态链接
// 静态链接很重要：避免沙箱中缺少动态库
func runCpp(workDir, code, input string, timeLimit, memoryLimit int) Result {
	// 1. 将用户代码写入临时目录
	srcPath := filepath.Join(workDir, "main.cpp")
	binPath := filepath.Join(workDir, "main")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: "failed to write source file"}
	}

	// 2. 编译（带 30 秒超时，防止编译阶段死循环）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "g++", "-O2", "-std=c++17", "-static", "-o", binPath, srcPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{Status: StatusCompileError, ErrorMsg: stderr.String()}
	}

	// 3. 在沙箱中运行（具体隔离见 internal/sandbox/sandbox.go）
	cfg := sandbox.Config{
		BinaryPath:    binPath,
		Args:          nil,
		Stdin:         input,
		TimeLimitMs:   timeLimit,
		MemoryLimitMB: memoryLimit,
		PidsMax:       16, // C/C++ 程序通常是单进程，设 16 足够
	}
	r, err := sandbox.Run(cfg)
	if err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: fmt.Sprintf("sandbox error: %v", err)}
	}
	return sandboxResultToJudgeResult(r)
}

// runPython 直接解释执行 Python 代码（无需编译）
// Python 用 "-c" 参数直接传代码字符串，避免写文件
func runPython(code, input string, timeLimit, memoryLimit int) Result {
	cfg := sandbox.Config{
		BinaryPath:    "/usr/bin/python3",
		Args:          []string{"-c", code},
		Stdin:         input,
		TimeLimitMs:   timeLimit,
		MemoryLimitMB: memoryLimit,
		PidsMax:       16,
	}
	r, err := sandbox.Run(cfg)
	if err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: fmt.Sprintf("sandbox error: %v", err)}
	}
	return sandboxResultToJudgeResult(r)
}

// runJava 编译并运行 Java 代码
// 注意：Java 要求文件名和类名匹配（Main.java → public class Main）
// JVM 多线程较多，PidsMax 设 32
func runJava(workDir, code, input string, timeLimit, memoryLimit int) Result {
	srcPath := filepath.Join(workDir, "Main.java")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: "failed to write source file"}
	}

	// 编译
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "javac", "-d", workDir, srcPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{Status: StatusCompileError, ErrorMsg: stderr.String()}
	}

	// 运行（通过 java -cp 指定 classpath）
	cfg := sandbox.Config{
		BinaryPath:    "/usr/bin/java",
		Args:          []string{"-cp", workDir, "Main"},
		Stdin:         input,
		TimeLimitMs:   timeLimit,
		MemoryLimitMB: memoryLimit,
		PidsMax:       32, // JVM 有 GC 线程、编译器线程等，需要更多进程
	}
	r, err := sandbox.Run(cfg)
	if err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: fmt.Sprintf("sandbox error: %v", err)}
	}
	return sandboxResultToJudgeResult(r)
}

// runGo 编译并运行 Go 代码
// Go 编译产物是静态链接的，对沙箱友好
func runGo(workDir, code, input string, timeLimit, memoryLimit int) Result {
	srcPath := filepath.Join(workDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: "failed to write source file"}
	}

	binPath := filepath.Join(workDir, "main")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, srcPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{Status: StatusCompileError, ErrorMsg: stderr.String()}
	}

	cfg := sandbox.Config{
		BinaryPath:    binPath,
		Args:          nil,
		Stdin:         input,
		TimeLimitMs:   timeLimit,
		MemoryLimitMB: memoryLimit,
		PidsMax:       16,
	}
	r, err := sandbox.Run(cfg)
	if err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: fmt.Sprintf("sandbox error: %v", err)}
	}
	return sandboxResultToJudgeResult(r)
}

// runRust 编译并运行 Rust 代码
// Rust 编译比 C++ 慢，超时设为 60 秒
func runRust(workDir, code, input string, timeLimit, memoryLimit int) Result {
	srcPath := filepath.Join(workDir, "main.rs")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: "failed to write source file"}
	}

	// Rust 编译较慢（LLVM 后端），超时给 60 秒
	binPath := filepath.Join(workDir, "main")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rustc", "-O", "-o", binPath, srcPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{Status: StatusCompileError, ErrorMsg: stderr.String()}
	}

	cfg := sandbox.Config{
		BinaryPath:    binPath,
		Args:          nil,
		Stdin:         input,
		TimeLimitMs:   timeLimit,
		MemoryLimitMB: memoryLimit,
		PidsMax:       16,
	}
	r, err := sandbox.Run(cfg)
	if err != nil {
		return Result{Status: StatusRuntimeError, ErrorMsg: fmt.Sprintf("sandbox error: %v", err)}
	}
	return sandboxResultToJudgeResult(r)
}

// ============================================================================
// 辅助函数
// ============================================================================

// sandboxResultToJudgeResult 将沙箱运行结果转换为判题结果
// 沙箱返回的是 runResult（包含退出码、信号、时间等），需要映射到判题状态
func sandboxResultToJudgeResult(r *sandbox.RunResult) Result {
	status, timeUsed, memUsed := sandbox.ParseResult(r)

	result := Result{
		TimeUsed:   timeUsed,
		MemoryUsed: memUsed,
		Output:     r.Stdout,
	}

	switch status {
	case "Accepted":
		result.Status = StatusAccepted
	case "Time Limit Exceeded":
		result.Status = StatusTimeLimitExceeded
	case "Memory Limit Exceeded":
		result.Status = StatusMemoryLimitExceeded
	case "Runtime Error":
		result.Status = StatusRuntimeError
		result.ErrorMsg = r.Stderr
	default:
		result.Status = StatusRuntimeError
		result.ErrorMsg = r.Stderr
	}
	return result
}

// NormalizeOutput 标准化输出，用于判题时的输出比较
// 规则：去掉头尾空白 → 每行去尾部空白 → 去掉末尾空行
// 这样 "hello\n" 和 "hello" 视为相同，避免空白差异导致误判
func NormalizeOutput(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// CompareOutput 比较预期输出和实际输出
// 返回 nil 表示一致（Accepted），返回 error 表示不一致（Wrong Answer）
// error 信息包含两段对比，方便调试
func CompareOutput(expected, actual string) error {
	exp := NormalizeOutput(expected)
	act := NormalizeOutput(actual)
	if exp != act {
		return fmt.Errorf("output mismatch:\n--- expected ---\n%s\n--- actual ---\n%s", exp, act)
	}
	return nil
}
