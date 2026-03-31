package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"feedsystem_video_go/internal/middleware/redis"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/tools"
)

type AIChatService struct {
	llmModel llms.Model
	tools    []tools.Tool // 👈 核心改变：使用工具接口的切片
	cache    *redis.Client
}
type RedisMessage struct {
	Type    string `json:"type"`    // 角色类型：human, ai, system
	Content string `json:"content"` // 对话内容
}

// NewAIChatService 使用可变参数（...）来接收任意数量的工具
func NewAIChatService(llmModel llms.Model, opts ...tools.Tool) *AIChatService {
	return &AIChatService{
		llmModel: llmModel,
		tools:    opts, // 可以是 0 个，也可以是 100 个
	}
}

// ChatWithAgent 核心对话接口
func (s *AIChatService) ChatWithAgent(ctx context.Context, userInput string, sessionId uint) (string, error) {
	// 1. 定义你专属的 System Prompt
	mySystemPrompt := `你是一个专业的游戏视频平台 AI 助手, 你可以帮助用户搜索视频、查询天气等。请根据用户的提问，合理调用工具来获取信息，并给出准确的回答。`
	rawHistory, _ := s.cache.Rdb.Get(ctx, fmt.Sprintf("session:%d", sessionId)).Result()
	// 2. 将字符串还原为 LangChain 的 ChatMessage 列表
	historyMessages := parseHistory(rawHistory)
	// 2. 核心改变：先创建 Agent (代替原来的 ZeroShotReactDescription)
	// 这里专门负责注入模型、工具和你的专属 Prompt
	agent := agents.NewOneShotAgent(
		s.llmModel,
		s.tools,
		agents.WithPromptPrefix(mySystemPrompt),
	)
	chatHistory := memory.NewChatMessageHistory(
		memory.WithPreviousMessages(historyMessages),
	)
	// 3. 创建基于历史记录的 Memory
	// 注意：这里需要实现一个适配器把 messages 塞进 Buffer
	mem := memory.NewConversationBuffer(memory.WithChatHistory(chatHistory))
	// 3. 核心改变：创建 Executor 执行器
	// 这里专门负责控制运行逻辑，比如最大循环次数 (MaxIterations)
	executor := agents.NewExecutor(
		agent,
		agents.WithMaxIterations(3),
		agents.WithMemory(mem),
	)

	log.Printf("🤖 [Agent 启动] 接收到提问: %s", userInput)

	// 4. 运行逻辑保持不变
	answer, err := chains.Run(ctx, executor, userInput, chains.WithCallback(callbacks.LogHandler{}))
	if err != nil {
		fmt.Printf("❌ 大模型调用失败: %v\n", err)
		return "", fmt.Errorf("Agent 运行异常: %w", err)
	}

	log.Printf("✅ [Agent 完毕] 返回结果: %s", answer)
	newMessages, _ := mem.ChatHistory.Messages(ctx)
	s.cache.Rdb.Set(ctx, fmt.Sprintf("session:%d", sessionId), toJSON(newMessages), 24*time.Hour)
	return answer, nil
}

// parseHistory：从 Redis 读取 JSON 字符串，还原为 LangChain 的接口切片
func parseHistory(rawJSON string) []llms.ChatMessage {
	var messages []llms.ChatMessage

	// 1. 如果是新用户，Redis 里没数据，直接返回空切片
	if rawJSON == "" {
		return messages
	}

	// 2. 先把 JSON 字符串反序列化为我们自定义的中间结构体切片
	var redisMsgs []RedisMessage
	if err := json.Unmarshal([]byte(rawJSON), &redisMsgs); err != nil {
		log.Printf("⚠️ 反序列化历史记录失败 (数据可能被污染): %v", err)
		return messages // 即使报错，也返回空切片，保证流程不崩溃
	}

	// 3. 遍历中间结构体，根据 Type 还原成 LangChainGo 对应的具体类型
	for _, msg := range redisMsgs {
		switch msg.Type {
		case "human":
			// 还原为用户的消息类型
			messages = append(messages, llms.HumanChatMessage{Content: msg.Content})
		case "ai":
			// 还原为大模型的回复类型
			messages = append(messages, llms.AIChatMessage{Content: msg.Content})
		case "system":
			// 还原为系统提示词类型
			messages = append(messages, llms.SystemChatMessage{Content: msg.Content})
		default:
			log.Printf("⚠️ 忽略未知的消息类型: %s", msg.Type)
		}
	}

	return messages
}

// toJSON：把 LangChain 的接口切片，打包成 JSON 字符串存入 Redis
func toJSON(messages []llms.ChatMessage) string {
	var redisMsgs []RedisMessage

	for _, msg := range messages {
		// msg.GetType() 是 LangChain 提供的接口方法
		switch msg.GetType() {
		case llms.ChatMessageTypeHuman:
			redisMsgs = append(redisMsgs, RedisMessage{Type: "human", Content: msg.GetContent()})
		case llms.ChatMessageTypeAI:
			redisMsgs = append(redisMsgs, RedisMessage{Type: "ai", Content: msg.GetContent()})
		case llms.ChatMessageTypeSystem:
			redisMsgs = append(redisMsgs, RedisMessage{Type: "system", Content: msg.GetContent()})
		}
	}

	// 序列化成 JSON 字符串
	bytes, err := json.Marshal(redisMsgs)
	if err != nil {
		log.Printf("⚠️ 序列化历史记录失败: %v", err)
		return "[]" // 如果失败，返回一个空数组的 JSON 字符串
	}

	return string(bytes)
}
