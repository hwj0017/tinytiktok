package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
)

type AIChatService struct {
	llmModel llms.Model
	tools    []tools.Tool // 👈 核心改变：使用工具接口的切片
}

// NewAIChatService 使用可变参数（...）来接收任意数量的工具
func NewAIChatService(llmModel llms.Model, opts ...tools.Tool) *AIChatService {
	return &AIChatService{
		llmModel: llmModel,
		tools:    opts, // 可以是 0 个，也可以是 100 个
	}
}

// ChatWithAgent 核心对话接口
func (s *AIChatService) ChatWithAgent(ctx context.Context, userInput string) (string, error) {
	// 1. 定义你专属的 System Prompt (这就是 Prefix)
	mySystemPrompt := `你是一个专业的游戏视频平台 AI 助手。警告：当你输出完 Action 和 Action Input 后，严禁自行生成 Observation！`

	// 2. 初始化 Agent，并把你的 Prompt 注入进去
	executor, err := agents.Initialize(
		s.llmModel,
		s.tools,
		agents.ZeroShotReactDescription, // 依然使用 ReAct 框架
		agents.WithMaxIterations(3),
		// 👇 核心代码：替换默认的系统提示词开头！
		agents.WithPromptPrefix(mySystemPrompt),
	)
	if err != nil {
		return "", fmt.Errorf("初始化 Agent 失败: %w", err)
	}

	log.Printf("🤖 [Agent 启动] 接收到提问: %s", userInput)

	answer, err := chains.Run(ctx, executor, userInput, chains.WithCallback(callbacks.LogHandler{}))
	if err != nil {
		fmt.Printf("❌ 大模型调用失败: %v\n", err)
		return "", fmt.Errorf("Agent 运行异常: %w", err)
	}

	log.Printf("✅ [Agent 完毕] 返回结果: %s", answer)
	return answer, nil
}
