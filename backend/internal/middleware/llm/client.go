package llm

import (
	"feedsystem_video_go/internal/config"
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// NewLLM 根据配置初始化并返回一个标准的 llms.Model 接口实例
func NewLLM(cfg config.LLMConfig) (llms.Model, error) {
	// 将配置一次性注入到底层实例中
	opts := []openai.Option{
		openai.WithToken(cfg.APIKey),
		openai.WithBaseURL(cfg.BaseURL),
		openai.WithModel(cfg.Model), // 直接把模型名称绑死在客户端上
	}

	// openai.New 返回的 *openai.LLM 天生实现了 llms.Model 接口
	llm, err := openai.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("初始化远端大模型失败: %w", err)
	}

	return llm, nil
}
