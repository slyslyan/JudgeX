package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/ai"
	"judgex/internal/bpf"
	"judgex/internal/database"
	"judgex/internal/diagnostics"
	"judgex/internal/metrics"
	"judgex/internal/model"
	"judgex/internal/queue"
)

// ============================================================================
// SRE Ops Agent
// ============================================================================
//
// SRE Ops Agent 是一个 ReAct 风格（Reasoning + Acting）的运维 AI Agent。
// 它不像其他 AI Agent 那样直接对话，而是：
// 1. 分析用户消息 → 识别意图 → 调用对应工具
// 2. 执行工具（获取系统指标、告警、重启节点、生成报告）
// 3. 将工具结果发给 LLM → LLM 分析并给出运维建议
//
// 可用工具：
//   - getSystemMetrics: 获取系统实时快照（队列、沙箱、数据库、提交统计）
//   - getAlerts: 通过 6 条规则检测系统异常（队列深度、错误率、沙箱、DB、磁盘、提交异常）
//   - restartJudgeNode: 重启评测节点（Docker → systemd 回退）
//   - generateReport: 生成 24 小时运行报告（7 个维度的 SQL 聚合查询）
//   - getBPFMetrics: 获取 eBPF 网络监控数据（延迟、拓扑、异常流量）

const sreAgentTimeout = 120 * time.Second

type sreAgentRequest struct {
	Message string `json:"message" binding:"required"`
}

type toolCall struct {
	Name string      `json:"name"`
	Args interface{} `json:"args"`
}

type toolResult struct {
	Tool    string          `json:"tool"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error,omitempty"`
}

// ============================================================================
// SRE Agent 主入口
// ============================================================================

// SREAgentChat 处理 POST /api/admin/sre/agent。
//
// 工作流程：
// 1. SSE 连接初始化
// 2. 检测用户消息中的关键词，决定调用哪些工具
// 3. 串行执行每个工具（步骤间推送状态事件）
// 4. 将所有工具结果组装成 LLM 上下文
// 5. 调用 LLM 分析并流式输出建议
//
// SSE 事件类型：
//   - "status": 正在执行的步骤
//   - "tool_result": 工具执行结果（JSON）
//   - "token": LLM 输出片段
//   - "error": 错误信息
//   - "done": 响应完成
func SREAgentChat(c *gin.Context) {
	var req sreAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// SSE 初始化
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	writeSSE := func(event, data string) {
		if event != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
		} else {
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		}
		flusher.Flush()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), sreAgentTimeout)
	defer cancel()

	// 第一步：检测用户意图，确定要调用的工具
	toolsToCall := detectTools(req.Message)

	// 第二步：串行执行工具
	var toolResults []toolResult
	for _, tool := range toolsToCall {
		writeSSE("status", fmt.Sprintf("正在执行: %s ...", toolNameChinese(tool.Name)))

		var result toolResult
		switch tool.Name {
		case "getSystemMetrics":
			result = executeGetSystemMetrics()
		case "getAlerts":
			result = executeGetAlerts()
		case "restartJudgeNode":
			result = executeRestartJudgeNode(tool.Args)
		case "generateReport":
			result = executeGenerateReport(tool.Args)
		case "getBPFMetrics":
			result = executeGetBPFMetrics()
		default:
			result = toolResult{Tool: tool.Name, Success: false, Error: "未知工具"}
		}

		toolResults = append(toolResults, result)

		// 推送工具执行结果
		dataJSON, _ := json.Marshal(result)
		writeSSE("tool_result", string(dataJSON))
	}

	// 第三步：将工具结果发送给 LLM 分析
	writeSSE("status", "AI 正在分析...")

	llmCtx := buildSREAgentContext(req.Message, toolResults)
	systemPrompt := buildSREAgentSystemPrompt()

	messages := []ai.ChatMessage{
		{Role: "user", Content: llmCtx},
	}

	llmCtxTimeout, llmCancel := context.WithTimeout(ctx, 60*time.Second)
	defer llmCancel()

	ch := ai.StreamChat(llmCtxTimeout, systemPrompt, messages)

	for chunk := range ch {
		if chunk.Error != "" {
			log.Printf("[sre-agent] LLM error: %s", chunk.Error)
			writeSSE("error", fmt.Sprintf(`{"message":"AI 分析失败: %s"}`, chunk.Error))
			writeSSE("done", "")
			return
		}
		if chunk.Done {
			break
		}
		writeSSE("token", chunk.Token)
	}

	writeSSE("done", "")
}

// ============================================================================
// 工具调度引擎
// ============================================================================

// detectTools 根据用户消息中的关键词决定调用哪些工具。
//
// 关键词匹配规则：
// - 报告/统计类 → getSystemMetrics + generateReport
// - 重启/恢复类 → restartJudgeNode
// - 告警/异常类 → getAlerts + getSystemMetrics
// - eBPF/网络类 → getBPFMetrics + getSystemMetrics
// - 指标/健康类 → getSystemMetrics
// - 默认 → getSystemMetrics
func detectTools(message string) []toolCall {
	msg := strings.ToLower(message)

	var tools []toolCall

	// 报告类关键词
	if containsAny(msg, "报告", "报表", "report", "提交数", "提交量", "通过率", "活跃度", "统计") {
		tools = append(tools, toolCall{Name: "getSystemMetrics", Args: nil})
		tools = append(tools, toolCall{Name: "generateReport", Args: map[string]interface{}{
			"start_time": time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			"end_time":   time.Now().Format(time.RFC3339),
		}})
		return tools
	}

	// 重启类关键词
	if containsAny(msg, "重启", "restart", "恢复", "重试") {
		nodeID := extractNodeID(msg)
		if nodeID == "" {
			nodeID = "judge-worker-1"
		}
		tools = append(tools, toolCall{Name: "restartJudgeNode", Args: map[string]interface{}{
			"node_id": nodeID,
		}})
		return tools
	}

	// 告警类关键词
	if containsAny(msg, "告警", "alert", "报警", "异常", "故障") {
		tools = append(tools, toolCall{Name: "getAlerts", Args: nil})
		tools = append(tools, toolCall{Name: "getSystemMetrics", Args: nil})
		return tools
	}

	// eBPF/网络类关键词
	if containsAny(msg, "ebpf", "网络", "network", "流量", "traffic", "拓扑", "topology", "调用链") {
		tools = append(tools, toolCall{Name: "getBPFMetrics", Args: nil})
		tools = append(tools, toolCall{Name: "getSystemMetrics", Args: nil})
		return tools
	}

	// 指标/健康类关键词
	if containsAny(msg, "指标", "metrics", "健康", "health", "状态", "延迟", "latency", "cpu", "内存", "memory", "队列", "queue", "负载") {
		tools = append(tools, toolCall{Name: "getSystemMetrics", Args: nil})
		return tools
	}

	// 默认：获取系统指标
	tools = append(tools, toolCall{Name: "getSystemMetrics", Args: nil})
	return tools
}

// ============================================================================
// 工具实现
// ============================================================================

// executeGetSystemMetrics 获取系统实时快照。
// 调用 diagnostics.Collect() 收集队列/沙箱/数据库/提交等全方位状态。
func executeGetSystemMetrics() toolResult {
	_, bufLen, _, workerCount := queue.Stats()
	snap := diagnostics.Collect(bufLen, workerCount)

	data, err := json.Marshal(snap)
	if err != nil {
		return toolResult{Tool: "getSystemMetrics", Success: false, Error: err.Error()}
	}

	return toolResult{Tool: "getSystemMetrics", Success: true, Data: data}
}

// executeGetAlerts 通过 6 条内置规则检测系统异常。
//
// 告警规则：
// 1. 队列深度 > 100 → CRITICAL；> 20 → WARNING
// 2. 错误率 > 20% → WARNING
// 3. 沙箱异常 → CRITICAL
// 4. 数据库断开 → CRITICAL
// 5. 磁盘 < 10% → CRITICAL；< 20% → WARNING
// 6. 无异常 → INFO（AllClear）
func executeGetAlerts() toolResult {
	var alerts []map[string]interface{}

	_, bufLen, _, workerCount := queue.Stats()
	snap := diagnostics.Collect(bufLen, workerCount)

	// 队列深度检测
	if snap.Queue.LocalBufLen > 100 {
		alerts = append(alerts, map[string]interface{}{
			"name":        "JudgeQueueDepthCritical",
			"severity":    "critical",
			"message":     fmt.Sprintf("评测队列深度 %d，超过临界阈值 100", snap.Queue.LocalBufLen),
			"status":      "firing",
			"observed_at": time.Now().Format(time.RFC3339),
		})
	} else if snap.Queue.LocalBufLen > 20 {
		alerts = append(alerts, map[string]interface{}{
			"name":        "JudgeQueueDepthHigh",
			"severity":    "warning",
			"message":     fmt.Sprintf("评测队列深度 %d，超过警告阈值 20", snap.Queue.LocalBufLen),
			"status":      "firing",
			"observed_at": time.Now().Format(time.RFC3339),
		})
	}

	// 错误率检测（最近一小时）
	if snap.Submissions.LastHourTotal > 0 {
		errorRate := 1.0 - snap.Submissions.AcceptRate/100.0
		if errorRate > 0.20 {
			alerts = append(alerts, map[string]interface{}{
				"name":        "HighSubmissionErrorRate",
				"severity":    "warning",
				"message":     fmt.Sprintf("提交错误率 %.1f%%，超过阈值 20%%", errorRate*100),
				"status":      "firing",
				"observed_at": time.Now().Format(time.RFC3339),
			})
		}
	}

	// 沙箱状态检测
	if snap.Sandbox.Status != "healthy" {
		alerts = append(alerts, map[string]interface{}{
			"name":        "SandboxNotHealthy",
			"severity":    "critical",
			"message":     fmt.Sprintf("沙箱状态异常: %s", snap.Sandbox.Status),
			"status":      "firing",
			"observed_at": time.Now().Format(time.RFC3339),
		})
	}

	// 数据库连接检测
	if !snap.Database.Connected {
		alerts = append(alerts, map[string]interface{}{
			"name":        "DatabaseDown",
			"severity":    "critical",
			"message":     "数据库连接断开",
			"status":      "firing",
			"observed_at": time.Now().Format(time.RFC3339),
		})
	}

	// 磁盘空间检测（通过 Prometheus metrics 读取）
	diskFree := metrics.GetDiskFreePercent()
	if diskFree < 10 {
		alerts = append(alerts, map[string]interface{}{
			"name":        "DiskSpaceCritical",
			"severity":    "critical",
			"message":     fmt.Sprintf("磁盘剩余空间 %d%%，低于临界阈值 10%%", diskFree),
			"status":      "firing",
			"observed_at": time.Now().Format(time.RFC3339),
		})
	} else if diskFree < 20 {
		alerts = append(alerts, map[string]interface{}{
			"name":        "DiskSpaceLow",
			"severity":    "warning",
			"message":     fmt.Sprintf("磁盘剩余空间 %d%%，低于警告阈值 20%%", diskFree),
			"status":      "firing",
			"observed_at": time.Now().Format(time.RFC3339),
		})
	}

	if len(alerts) == 0 {
		alerts = append(alerts, map[string]interface{}{
			"name":     "AllClear",
			"severity": "info",
			"message":  "系统运行正常，无活跃告警",
			"status":   "resolved",
		})
	}

	data, _ := json.Marshal(gin.H{"alerts": alerts, "count": len(alerts)})
	return toolResult{Tool: "getAlerts", Success: true, Data: data}
}

// executeRestartJudgeNode 重启评测节点。
// 尝试 Docker restart → systemctl restart → sudo systemctl restart 依次回退。
// 无论哪种方式成功都算成功。
func executeRestartJudgeNode(args interface{}) toolResult {
	nodeID := ""
	if argsMap, ok := args.(map[string]interface{}); ok {
		if id, ok := argsMap["node_id"].(string); ok {
			nodeID = id
		}
	}
	if nodeID == "" {
		nodeID = "judge-worker-1"
	}

	// 按优先级尝试三个重启命令
	cmds := []string{
		fmt.Sprintf("docker restart %s 2>&1", nodeID),
		fmt.Sprintf("systemctl restart %s 2>&1", nodeID),
		fmt.Sprintf("sudo systemctl restart judge-worker 2>&1"),
	}

	var output string
	var success bool
	for _, cmdStr := range cmds {
		cmd := exec.Command("sh", "-c", cmdStr)
		out, err := cmd.CombinedOutput()
		if err == nil {
			success = true
			output = string(out)
			break
		}
		output = string(out)
	}

	if !success {
		return toolResult{
			Tool:    "restartJudgeNode",
			Success: false,
			Error:   fmt.Sprintf("重启 %s 失败: %s", nodeID, output),
		}
	}

	// 记录重启操作到 Prometheus metrics
	metrics.IncNodeRestart(nodeID)

	data, _ := json.Marshal(gin.H{
		"node_id": nodeID,
		"message": fmt.Sprintf("评测节点 %s 已重启", nodeID),
		"output":  strings.TrimSpace(output),
	})
	return toolResult{Tool: "restartJudgeNode", Success: true, Data: data}
}

// executeGetBPFMetrics 获取 eBPF 网络监控数据。
// 通过 HTTP 请求 eBPF 追踪器的 Prometheus 端点获取。
func executeGetBPFMetrics() toolResult {
	m := bpf.FetchMetrics()

	data, err := json.Marshal(m)
	if err != nil {
		return toolResult{Tool: "getBPFMetrics", Success: false, Error: err.Error()}
	}

	return toolResult{Tool: "getBPFMetrics", Success: true, Data: data}
}

// executeGenerateReport 生成指定时间段的运行报告。
// 调用 generateHistoricalReport() 执行 7 个维度的 SQL 聚合查询。
func executeGenerateReport(args interface{}) toolResult {
	startStr := ""
	endStr := ""

	if argsMap, ok := args.(map[string]interface{}); ok {
		if s, ok := argsMap["start_time"].(string); ok {
			startStr = s
		}
		if e, ok := argsMap["end_time"].(string); ok {
			endStr = e
		}
	}

	startTime := time.Now().Add(-24 * time.Hour)
	endTime := time.Now()

	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = t
		}
	}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = t
		}
	}

	report := generateHistoricalReport(startTime, endTime)
	data, _ := json.Marshal(report)

	return toolResult{Tool: "generateReport", Success: true, Data: data}
}

// ============================================================================
// 历史报告生成
// ============================================================================

type historicalReport struct {
	Period        string           `json:"period"`
	StartTime     string           `json:"start_time"`
	EndTime       string           `json:"end_time"`
	TotalSubs     int64            `json:"total_submissions"`
	AcceptedSubs  int64            `json:"accepted_submissions"`
	AcceptRate    float64          `json:"accept_rate"`
	ByLanguage    map[string]int64 `json:"by_language"`
	ByStatus      map[string]int64 `json:"by_status"`
	ByProblem     []problemStat    `json:"by_problem"`
	HourlyBuckets []hourlyBucket   `json:"hourly_buckets"`
	AvgLatency    float64          `json:"avg_latency_ms"`
	PeakTime      string           `json:"peak_hour"`
	PeakCount     int64            `json:"peak_count"`
}

type problemStat struct {
	ProblemID    uint   `json:"problem_id"`
	ProblemTitle string `json:"problem_title"`
	Submissions  int64  `json:"submissions"`
	Accepted     int64  `json:"accepted"`
}

type hourlyBucket struct {
	Hour   string `json:"hour"`
	Total  int64  `json:"total"`
	Accept int64  `json:"accept"`
}

// generateHistoricalReport 生成 7 个维度的系统运行报告。
//
// 报告维度：
// 1. 总提交数
// 2. 通过数 + 通过率
// 3. 按编程语言统计
// 4. 按判题状态统计
// 5. 按题目统计（Top 10）
// 6. 按小时统计（峰值检测）
// 7. 平均延迟（当前为占位符）
func generateHistoricalReport(startTime, endTime time.Time) historicalReport {
	report := historicalReport{
		Period:    fmt.Sprintf("%s ~ %s", startTime.Format("2006-01-02 15:04"), endTime.Format("2006-01-02 15:04")),
		StartTime: startTime.Format(time.RFC3339),
		EndTime:   endTime.Format(time.RFC3339),
	}

	if database.DB == nil {
		return report
	}

	// 1. 总提交数
	database.DB.Model(&model.Submission{}).
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Count(&report.TotalSubs)

	// 2. 通过数 + 通过率
	database.DB.Model(&model.Submission{}).
		Where("created_at BETWEEN ? AND ? AND status = ?", startTime, endTime, "Accepted").
		Count(&report.AcceptedSubs)

	if report.TotalSubs > 0 {
		report.AcceptRate = float64(report.AcceptedSubs) / float64(report.TotalSubs) * 100
	}

	// 3. 按语言统计
	type langCount struct {
		Language string
		Count    int64
	}
	var lcs []langCount
	database.DB.Model(&model.Submission{}).
		Select("language, count(*) as count").
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Group("language").Order("count DESC").Find(&lcs)
	report.ByLanguage = make(map[string]int64)
	for _, lc := range lcs {
		report.ByLanguage[lc.Language] = lc.Count
	}

	// 4. 按状态统计
	type statusCount struct {
		Status string
		Count  int64
	}
	var scs []statusCount
	database.DB.Model(&model.Submission{}).
		Select("status, count(*) as count").
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Group("status").Order("count DESC").Find(&scs)
	report.ByStatus = make(map[string]int64)
	for _, sc := range scs {
		report.ByStatus[sc.Status] = sc.Count
	}

	// 5. 按题目统计（Top 10）
	type problemCount struct {
		ProblemID uint
		Count     int64
	}
	var pcs []problemCount
	database.DB.Model(&model.Submission{}).
		Select("problem_id, count(*) as count").
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Group("problem_id").Order("count DESC").Limit(10).Find(&pcs)

	for _, pc := range pcs {
		ps := problemStat{
			ProblemID:   pc.ProblemID,
			Submissions: pc.Count,
		}
		database.DB.Model(&model.Submission{}).
			Where("problem_id = ? AND created_at BETWEEN ? AND ? AND status = ?",
				pc.ProblemID, startTime, endTime, "Accepted").
			Count(&ps.Accepted)

		var p model.Problem
		if err := database.DB.Select("title").First(&p, pc.ProblemID).Error; err == nil {
			ps.ProblemTitle = p.Title
		}
		report.ByProblem = append(report.ByProblem, ps)
	}

	// 6. 按小时统计（用于检测峰值时段）
	type hourlyCount struct {
		Hour   string
		Total  int64
		Accept int64
	}
	var hourlyData []hourlyCount

	database.DB.Raw(`
		SELECT
			DATE_FORMAT(created_at, '%Y-%m-%d %H:00') as hour,
			COUNT(*) as total,
			SUM(CASE WHEN status = 'Accepted' THEN 1 ELSE 0 END) as accept
		FROM submissions
		WHERE created_at BETWEEN ? AND ?
		GROUP BY hour
		ORDER BY hour
	`, startTime, endTime).Scan(&hourlyData)

	if len(hourlyData) > 0 {
		for _, h := range hourlyData {
			report.HourlyBuckets = append(report.HourlyBuckets, hourlyBucket{
				Hour:   h.Hour,
				Total:  h.Total,
				Accept: h.Accept,
			})
		}

		// 检测峰值小时
		maxTotal := int64(0)
		peakHour := ""
		for _, h := range hourlyData {
			if h.Total > maxTotal {
				maxTotal = h.Total
				peakHour = h.Hour
			}
		}
		report.PeakTime = peakHour
		report.PeakCount = maxTotal
	}

	// 7. 平均延迟（占位符——目前未按提交级别追踪延迟）
	report.AvgLatency = 0

	return report
}

// ============================================================================
// 辅助函数
// ============================================================================

// containsAny 检查字符串是否包含任意一个关键词。
func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// extractNodeID 从消息中提取评测节点 ID（judge-worker-1 ~ judge-worker-10）。
func extractNodeID(msg string) string {
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("judge-worker-%d", i)
		if strings.Contains(msg, id) {
			return id
		}
	}
	return ""
}

// toolNameChinese 返回工具的中文名称（用于 SSE status 事件展示）。
func toolNameChinese(name string) string {
	switch name {
	case "getSystemMetrics":
		return "获取系统指标"
	case "getAlerts":
		return "查询系统告警"
	case "restartJudgeNode":
		return "重启评测节点"
	case "generateReport":
		return "生成运行报告"
	case "getBPFMetrics":
		return "获取eBPF网络监控"
	default:
		return name
	}
}

// buildSREAgentContext 组装工具执行结果和用户问题，构造 LLM 上下文。
func buildSREAgentContext(message string, results []toolResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## 管理员问题\n%s\n\n", message))

	b.WriteString("## 工具执行结果\n\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("### 工具: %s\n", r.Tool))
		if !r.Success {
			b.WriteString(fmt.Sprintf("执行失败: %s\n\n", r.Error))
		} else if r.Data != nil {
			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, r.Data, "", "  "); err == nil {
				b.WriteString("```json\n" + prettyJSON.String() + "\n```\n\n")
			} else {
				b.WriteString("```json\n" + string(r.Data) + "\n```\n\n")
			}
		}
	}

	b.WriteString(`请根据以上数据回答管理员的问题。用中文回答，保持专业和简洁。
如果是报告类问题，按以下结构回答：
1. **概览** - 总体状况
2. **关键指标** - 提交量、通过率、活跃度
3. **问题分析** - 如果发现问题，分析原因
4. **建议** - 改进建议

如果执行了重启操作，报告重启结果。
如果查询了告警，分析告警的严重程度和影响范围。`)

	return b.String()
}

// buildSREAgentSystemPrompt 构建 SRE Agent 的系统提示词。
func buildSREAgentSystemPrompt() string {
	return `你是 JudgeX 在线评测系统的 SRE 运维 Agent，负责系统监控和运维管理。

## 可用工具
你通过以下工具获取信息并执行操作：

1. **getSystemMetrics()** - 获取系统实时指标（队列深度、沙箱状态、数据库连接、提交统计、错误分布）
2. **getAlerts()** - 获取当前系统告警（队列深度告警、错误率告警、磁盘空间告警、沙箱/数据库异常）
3. **restartJudgeNode(nodeId)** - 重启指定的评测节点。通常在评测延迟突增或节点异常时使用。
4. **generateReport(startTime, endTime)** - 生成指定时间段内的系统运行报表。
5. **getBPFMetrics()** - 获取 eBPF 网络监控数据（实时调用拓扑、延迟分布、异常流量、自愈动作）。数据来自内核级 eBPF 追踪器。

## 职责
1. **监控分析** - 分析系统指标，发现异常
2. **故障处理** - 判断是否需要重启节点或干预
3. **报告生成** - 生成运维报告，总结系统运行状况
4. **告警响应** - 分析告警原因，给出处理建议

## 输出规范
- 用中文回答
- 结构化输出，使用 Markdown 格式
- 对异常指标给出具体的数值和建议
- 如果是严重问题，明确标注 ⚠️ 或 🚨
- 保持回答简洁专业，不啰嗦`
}
