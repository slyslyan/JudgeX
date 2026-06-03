package bpf

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// eBPF 追踪器指标采集
// ============================================================================
//
// bpf 包与 K3s 集群中部署的 eBPF 追踪器通信，获取系统调用级别的监控数据。
// eBPF（extended Berkeley Packet Filter）是一种在内核中运行沙箱程序的技术，
// 可用于安全监控、性能分析等场景。
//
// 在本项目中，eBPF 追踪器的作用：
//   - 追踪网络连接延迟（src → dst 的延迟和异常分数）
//   - 检测异常行为（基于延迟异常分数）
//   - 自动执行缓解措施（如重启异常 Pod）
//
// 数据来源：
//   eBPF 追踪器通过 Prometheus 格式暴露指标，地址由 EBPF_TRACER_URL 环境变量指定。
//   本程序定期抓取并解析这些指标，供 SRE 面板展示。
//
// 采集的指标：
//   - ebpf_agent_up              — 追踪器是否在线（1/0）
//   - ebpf_agent_events_total    — 已处理事件总数
//   - ebpf_agent_errors_total    — 错误总数
//   - ebpf_edge_anomaly_score    — 每条边的异常分数（>0 表示异常）
//   - ebpf_mitigation_total      — 自动缓解措施计数

// Metrics 保存 eBPF 追踪器指标的快照。
type Metrics struct {
	Enabled      bool              `json:"enabled"`       // 是否启用了 eBPF 监控
	TracerURL    string            `json:"tracer_url"`    // 追踪器地址
	Up           int               `json:"up"`            // 1=在线, 0=离线
	EventsTotal  int64             `json:"events_total"`  // 总事件数
	ErrorsTotal  int64             `json:"errors_total"`  // 总错误数
	AnomalyCount int               `json:"anomaly_count"` // 异常边数（分数 > 0）
	EdgeCount    int               `json:"edge_count"`    // 被追踪的网络边总数
	TopLatency   []LatencyEntry    `json:"top_latency"`   // 延迟异常最高的前 5 条边
	Mitigations  []MitigationEntry `json:"mitigations"`   // 缓解措施记录
	Status       string            `json:"status"`        // 整体状态：healthy/degraded/down
	Error        string            `json:"error,omitempty"`
	FetchedAt    time.Time         `json:"fetched_at"` // 采集时间
}

// LatencyEntry 表示一条网络边的延迟异常信息。
// Src 和 Dst 分别表示源和目标（如 IP:PORT 或进程名）。
type LatencyEntry struct {
	Src   string  `json:"src"`           // 源地址
	Dst   string  `json:"dst"`           // 目标地址
	Score float64 `json:"anomaly_score"` // 异常分数（越高越异常）
}

// MitigationEntry 表示一次自动缓解措施。
type MitigationEntry struct {
	IP     string `json:"ip"`     // 目标 IP
	Action string `json:"action"` // 措施（如 "pod_restart"）
	Count  int64  `json:"count"`  // 执行次数
}

// defaultURL 是 eBPF 追踪器 Prometheus 端点的默认地址。
// 可通过 EBPF_TRACER_URL 环境变量覆盖。
const defaultURL = "http://10.0.0.15:2112/metrics"

var tracerURL = defaultURL

func init() {
	if v := strings.TrimSpace(env("EBPF_TRACER_URL", "")); v != "" {
		tracerURL = v
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// FetchMetrics 抓取并解析 eBPF 追踪器的 Prometheus 指标。
// 如果追踪器不可达，返回包含错误信息的 Metrics 结构（不会 panic）。
func FetchMetrics() Metrics {
	m := Metrics{
		Enabled:   true,
		TracerURL: tracerURL,
		FetchedAt: time.Now(),
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(tracerURL)
	if err != nil {
		m.Status = "down"
		m.Error = fmt.Sprintf("无法连接 eBPF 追踪器: %v", err)
		return m
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		m.Status = "down"
		m.Error = fmt.Sprintf("读取 eBPF 指标失败: %v", err)
		return m
	}

	parseMetrics(string(body), &m)

	// 根据错误数判断健康状态
	if m.Up == 0 {
		m.Status = "down"
		m.Error = "eBPF 追踪器不在运行状态"
	} else if m.ErrorsTotal > 100 {
		m.Status = "degraded"
		m.Error = fmt.Sprintf("eBPF 追踪器错误率较高: %d 错误", m.ErrorsTotal)
	} else {
		m.Status = "healthy"
	}

	return m
}

// parseMetrics 解析 eBPF 追踪器的 Prometheus 文本格式指标。
//
// Prometheus 指标行格式：
//
//	# HELP ebpf_agent_up Agent uptime status
//	# TYPE ebpf_agent_up gauge
//	ebpf_agent_up 1
//	ebpf_edge_anomaly_score{dst="10.42.0.1:8080",src="curl"} 3.2
//
// 以 # 开头的是注释行，其他逐行解析。
func parseMetrics(text string, m *Metrics) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case strings.HasPrefix(line, "ebpf_agent_up"):
			m.Up = int(parseGaugeValue(line))

		case strings.HasPrefix(line, "ebpf_agent_events_total"):
			m.EventsTotal += parseCounterValue(line)

		case strings.HasPrefix(line, "ebpf_agent_errors_total"):
			m.ErrorsTotal += parseCounterValue(line)

		case strings.HasPrefix(line, "ebpf_edge_anomaly_score{") || strings.HasPrefix(line, "ebpf_edge_anomaly_score "):
			val := parseGaugeValue(line)
			if val > 0 {
				m.AnomalyCount++
				src, dst := extractLabels(line)
				if val > 0 && len(m.TopLatency) < 5 {
					m.TopLatency = append(m.TopLatency, LatencyEntry{
						Src: src, Dst: dst, Score: val,
					})
				}
			}
			m.EdgeCount++

		case strings.HasPrefix(line, "ebpf_mitigation_total{"):
			ip, action := extractMitigationLabels(line)
			count := parseCounterValue(line)
			if ip != "" && count > 0 {
				m.Mitigations = append(m.Mitigations, MitigationEntry{
					IP: ip, Action: action, Count: count,
				})
			}
		}
	}
}

// parseGaugeValue 解析 Prometheus gauge 行的数值（最后一段）。
// 例如 "ebpf_agent_up 1" → 1.0
func parseGaugeValue(line string) float64 {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return 0
	}
	valStr := parts[len(parts)-1]
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseCounterValue 解析 Prometheus counter 行的整数值。
func parseCounterValue(line string) int64 {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return 0
	}
	valStr := parts[len(parts)-1]
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0
	}
	return int64(v)
}

// extractLabels 从 Prometheus 标签字符串中提取 src 和 dst。
// 输入示例：ebpf_edge_anomaly_score{dst="127.0.0.1:8080",src="curl"} 3.2
// 输出：src="curl", dst="127.0.0.1:8080"
func extractLabels(line string) (src, dst string) {
	braceStart := strings.Index(line, "{")
	if braceStart < 0 {
		return "", ""
	}
	braceEnd := strings.Index(line, "}")
	if braceEnd < 0 || braceEnd <= braceStart {
		return "", ""
	}
	labels := line[braceStart+1 : braceEnd]
	for _, part := range strings.Split(labels, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(kv[1], "\"")
		switch key {
		case "src":
			src = val
		case "dst":
			dst = val
		}
	}
	return src, dst
}

// extractMitigationLabels 从缓解措施指标行中提取 ip 和 action。
// 输入示例：ebpf_mitigation_total{action="pod_restart",ip="10.42.0.63"} 3
// 输出：ip="10.42.0.63", action="pod_restart"
func extractMitigationLabels(line string) (ip, action string) {
	braceStart := strings.Index(line, "{")
	if braceStart < 0 {
		return "", ""
	}
	braceEnd := strings.Index(line, "}")
	if braceEnd < 0 || braceEnd <= braceStart {
		return "", ""
	}
	labels := line[braceStart+1 : braceEnd]
	for _, part := range strings.Split(labels, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(kv[1], "\"")
		switch key {
		case "ip":
			ip = val
		case "action":
			action = val
		}
	}
	return ip, action
}
