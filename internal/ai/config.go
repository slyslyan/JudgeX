package ai

import "os"

// ============================================================================
// LLM API 配置
// ============================================================================
//
// Config 存储连接到 LLM（大语言模型）服务所需的配置参数。
// 从环境变量加载，遵循 12-factor 配置原则。
//
// 环境变量：
//   LLM_API_URL — API 基础地址（默认：https://api.deepseek.com/v1）
//   LLM_API_KEY — API 密钥（代码中有一个默认 key，生产环境必须更换）
//   LLM_MODEL  — 模型名称（默认：deepseek-v4-flash）
//
// 支持兼容 OpenAI API 格式的任何 LLM 服务（DeepSeek、OpenAI、Anthropic 等），
// 只需提供对应的 BaseURL 和 APIKey 即可。

type Config struct {
	BaseURL string // API 基础地址（如 https://api.openai.com/v1）
	APIKey  string // API 认证密钥
	Model   string // 模型标识（如 gpt-4o-mini, deepseek-v4-flash）
}

// LoadConfig 从环境变量读取 LLM 配置。
// 使用 LoadConfig() 而不是 init() 是为了便于测试时替换配置。
func LoadConfig() Config {
	baseURL := os.Getenv("LLM_API_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		apiKey = "sk-604192a02470447a8a90c74e6e3a69c7"
	}
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "deepseek-v4-flash"
	}
	return Config{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}
}

// Cfg 是全局的 LLM 配置实例，在包初始化时加载。
// 其他包通过 ai.Cfg 访问 LLM 配置。
var Cfg = LoadConfig()
