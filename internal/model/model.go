package model

import (
	"encoding/json"
	"time"
)

// ============================================================================
// 数据模型（GORM 映射）
// ============================================================================
//
// 本文件定义 JudgeX 的所有数据库模型。
// GORM 使用结构体标签（gorm 标签）自动映射到 MySQL 表。
//
// 数据库 ER 关系概览：
//
//	User 1──* Submission *──1 Problem
//	  │                        │
//	  │                        ├── * TestCase (已废弃，改用磁盘文件)
//	  │                        │
//	  │                        └── * ProblemTag (多对多)
//	  │
//	Contest 1──* ContestProblem *──1 Problem
//	  │
//	  └── Submission (通过 ContestID 关联)

// ============================================================================
// User 用户
// ============================================================================

// User 代表系统用户。
//
// 表名：users
// 角色体系：user < admin < super_admin
//   - user: 普通用户，可以查看题目、提交代码、查看排行榜
//   - admin: 管理员，可以创建/编辑/删除题目和比赛
//   - super_admin: 超级管理员，可以管理用户角色和删除用户
//
// 重要字段说明：
//   - PasswordHash: 存储 bcrypt 哈希，不存储明文密码
//     json:"-" 确保密码哈希永远不会在 JSON 响应中泄露
//   - CodeTemplates: 用户保存的代码模板 JSON，存储每种语言的模板代码
//     格式：[{"language":"cpp","template":"#include <bits/stdc++.h>..."}, ...]
//   - Role: 默认 "user"，通过 "super_admin" 权限提升
type User struct {
	ID            uint            `gorm:"primaryKey" json:"id"`
	Username      string          `gorm:"uniqueIndex;size:64;not null" json:"username"`
	Email         string          `gorm:"size:128" json:"email"`
	Bio           string          `gorm:"size:512" json:"bio"`
	PasswordHash  string          `gorm:"size:255;not null" json:"-"` // json:"-" 防止泄露
	Role          string          `gorm:"size:16;default:user" json:"role"`
	CodeTemplates json.RawMessage `gorm:"type:json" json:"code_templates,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ============================================================================
// Problem 题目
// ============================================================================

// Problem 代表一道编程题目。
//
// 表名：problems
// 题目是判题系统的核心实体，包含题目的描述、限制条件和测试数据。
//
// 重要字段说明：
//   - Number: 题号，可排序（默认等于 ID，但 admin 可以修改）
//   - TimeLimit: CPU 时间限制，单位毫秒（默认 1000ms = 1秒）
//   - MemoryLimit: 内存限制，单位 MB（默认 128MB）
//   - SampleCases: 样例数据 JSON 数组
//     格式：[{"input":"3 5\n","output":"8\n"}, ...]
//     前端渲染为样例输入/输出框
//   - TestCaseVersion: 测试数据版本号，每次上传测试数据时递增
//     judge-worker 通过版本号判断是否需要重新加载测试数据
//   - AcceptedCount: 通过的提交数，用于排行榜和统计
//   - Tags: 题目标签，多对多关系（通过 problem_tag_links 关联表）
type Problem struct {
	ID                uint            `gorm:"primaryKey" json:"id"`
	Number            int             `gorm:"default:0;index" json:"number"`
	Title             string          `gorm:"size:255;not null" json:"title"`
	Description       string          `gorm:"type:text" json:"description"`
	TimeLimit         int             `gorm:"default:1000" json:"time_limit"`  // ms
	MemoryLimit       int             `gorm:"default:128" json:"memory_limit"` // MB
	SampleCases       json.RawMessage `gorm:"type:json" json:"sample_cases"`
	ReferenceSolution string          `gorm:"type:text" json:"reference_solution,omitempty"` // 参考解（用于TLE验证）
	TestCaseVersion   int             `gorm:"default:1" json:"test_case_version"`
	AcceptedCount     int64           `gorm:"default:0" json:"accepted_count"`
	Tags              []ProblemTag    `gorm:"many2many:problem_tag_links;joinForeignKey:ProblemID;joinReferences:TagID" json:"tags,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

// ProblemTag 题目标签。
//
// 表名：problem_tags
// 和 Problem 是多对多关系，中间表为 problem_tag_links。
// 如 "Math", "DP", "Graph", "String" 等分类标签。
type ProblemTag struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;size:64;not null" json:"name"`
}

// ============================================================================
// Submission 提交记录
// ============================================================================

// Submission 代表用户的代码提交记录。
//
// 表名：submissions
// 这是系统中最核心的数据——记录了用户每次提交代码的全部信息。
//
// 状态流转：
//
//	pending → (worker 消费) → judging → (沙箱执行完成) → 终态
//	                                                      ├── Accepted
//	                                                      ├── Wrong Answer
//	                                                      ├── Time Limit Exceeded
//	                                                      ├── Memory Limit Exceeded
//	                                                      └── Runtime Error
//
// 重要字段说明：
//   - UserID + ProblemID: 联合索引，用于快速查询某用户对某题的提交历史
//   - ContestID: 可选字段，如果提交来自比赛则关联比赛 ID
//   - Language: 编程语言（cpp / python / java / go / rust）
//   - Code: 用户提交的源代码（完整内容，不是文件路径）
//   - Status: 当前状态
//   - TimeUsed: 所有测试用例中的最大运行时间（毫秒）
//   - MemoryUsed: 所有测试用例中的最大内存使用（MB）
//   - PassedCount / TotalCases: 通过的测试用例数 / 总测试用例数
//     例如 7/10 表示 7 个通过、3 个失败
//   - ErrorMessage: 编译错误输出或运行时错误信息
//
// 索引说明：
//   - idx_user_status: (user_id, status)，用于快速查询用户的提交历史
//   - idx_user_problem_status: (user_id, problem_id, status)，用于判断是否已 AC
type Submission struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"index:idx_user_status,priority:1;index:idx_user_problem_status,priority:1;index:idx_user_problem_contest,priority:1;not null" json:"user_id"`
	ProblemID    uint      `gorm:"index;index:idx_user_problem_status,priority:2;index:idx_user_problem_contest,priority:2;not null" json:"problem_id"`
	ContestID    *uint     `gorm:"index;index:idx_user_problem_contest,priority:3" json:"contest_id,omitempty"`
	Language     string    `gorm:"size:16;not null" json:"language"`
	Code         string    `gorm:"type:text;not null" json:"code"`
	Status       string    `gorm:"size:32;default:pending;index:idx_status_user,priority:1;index:idx_user_status,priority:2;index:idx_user_problem_status,priority:3" json:"status"`
	TimeUsed     int       `gorm:"default:0" json:"time_used"`
	MemoryUsed   int       `gorm:"default:0" json:"memory_used"`
	PassedCount  int       `gorm:"default:0" json:"passed_count"`
	TotalCases   int       `gorm:"default:0" json:"total_cases"`
	ErrorMessage string    `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

// ============================================================================
// ProblemFeedback AI 对题目/测试数据的质量反馈
// ============================================================================

// ProblemFeedback 记录 AI Debug Agent 在分析过程中发现的题目质量问题。
//
// 优先级（Priority）说明：
//
//	P1（紧急）— 测试数据与描述矛盾、样例错误等客观可验证的数据错误
//	            管理员需要优先处理，因为会影响所有用户
//	P2（一般）— 题目描述有歧义、测试覆盖不足等主观判断
//	            可以在有空的时候再看，不紧急
//
// 触发场景：
//   - 问题描述有歧义，导致用户反复在特定测试点上 WA
//   - 样例输入/输出与描述不符
//   - 隐藏测试数据疑似有误（如约束与描述不一致）
//
// 避免幻觉的策略：
//   - 只保存 confidence="high" 的反馈
//   - 每条反馈必须有 evidence 字段，记录具体矛盾点
//   - 人工审核后才变为 "confirmed"
type ProblemFeedback struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	ProblemID    uint      `gorm:"index;not null" json:"problem_id"`
	UserID       uint      `gorm:"index;not null" json:"user_id"`
	SubmissionID int64     `json:"submission_id"`
	FeedbackType string    `gorm:"size:32;not null" json:"feedback_type"` // "unclear_description", "suspicious_testdata", "sample_error"
	Priority     string    `gorm:"size:4;default:P2" json:"priority"`     // "P1"=紧急(数据错误), "P2"=一般(表述问题)
	Description  string    `gorm:"type:text" json:"description"`          // AI 生成的问题描述
	Evidence     string    `gorm:"type:text" json:"evidence"`             // 具体证据（引用哪个测试点、哪段描述）
	Confidence   string    `gorm:"size:16;not null" json:"confidence"`    // "high" 或 "medium"，只有 high 才保存
	Status       string    `gorm:"size:16;default:pending" json:"status"` // "pending", "confirmed", "dismissed"
	CreatedAt    time.Time `json:"created_at"`
	ReviewNote   string    `gorm:"type:text" json:"review_note,omitempty"` // 管理员审核意见
}

// ============================================================================
// TestCase 测试用例（已废弃）
// ============================================================================

// TestCase 是旧版的测试用例模型（存储在 MySQL 中）。
//
// 已废弃原因：
//  1. 测试数据存储在 MySQL text 字段中，大数据量时查询慢
//  2. 难以管理二进制测试数据或大文件
//  3. 备份和迁移不方便
//
// 替代方案：
//
//	测试数据存储在磁盘文件 /data/testcases/{problem_id}/{N}.in/.out
//	或通过 S3/MinIO 对象存储管理。
//	详细信息见 internal/storage/storage.go。
//
// 保留此模型是为了向后兼容旧数据（通过 MySQL fallback 读取）。
type TestCase struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	ProblemID uint   `gorm:"index;not null" json:"problem_id"`
	Input     string `gorm:"type:text" json:"input"`
	Expected  string `gorm:"type:text" json:"expected"`
	IsExample bool   `gorm:"default:false" json:"is_example"`
}

// ============================================================================
// Contest 比赛
// ============================================================================

// Contest 代表一场编程比赛。
//
// 表名：contests
// 支持两种比赛规则：
//   - ACM 模式（ACM）：二进制判题，通过得满分，未通过 0 分
//     排名规则：按解题数降序，解题数相同时按总用时升序
//     罚时：每次错误提交加 20 分钟
//   - IOI 模式（IOI）：部分得分，按通过测试用例比例给分
//     排名规则：按总分降序，相同则用时少的排前面
//
// 状态计算：
//   - Not Started: time.Now() < StartTime
//   - Running: StartTime <= time.Now() < EndTime
//   - Ended: time.Now() >= EndTime
//
// 状态是动态计算的（每次请求时判断），不存储在数据库中。
type Contest struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Title       string    `gorm:"size:255;not null" json:"title"`
	Description string    `gorm:"type:text" json:"description"`
	StartTime   time.Time `gorm:"not null" json:"start_time"`
	EndTime     time.Time `gorm:"not null" json:"end_time"`
	RuleType    string    `gorm:"size:16;default:ACM" json:"rule_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// Announcement 公告。
//
// 表名：announcements
// 管理员可以在后台创建公告，展示在首页公告栏。
type Announcement struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `gorm:"size:255;not null" json:"title"`
	Content   string    `gorm:"type:text" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ContestProblem 比赛-题目关联表。
//
// 表名：contest_problems
// 每场比赛包含多道题目，每道题目在比赛中有一个显示编号（A, B, C, ...）。
// 这种设计解耦了题目在比赛中的展示顺序和题目本身的 ID。
type ContestProblem struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	ContestID uint   `gorm:"index;not null" json:"contest_id"`
	ProblemID uint   `gorm:"index;not null" json:"problem_id"`
	DisplayID string `gorm:"size:4;not null" json:"display_id"` // "A", "B", "C", ...
}
