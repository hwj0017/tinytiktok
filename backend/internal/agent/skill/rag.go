package skill

import (
	"context"
	"fmt"
	"strings"

	"feedsystem_video_go/internal/middleware/embedding"
	"feedsystem_video_go/internal/middleware/vectordb"
	"github.com/tmc/langchaingo/tools"
)

// 确保 SearchVideoTool 实现了 langchaingo 的 tools.Tool 接口
var _ tools.Tool = (*SearchVideoTool)(nil)

// SearchVideoTool 实现了 RAG 的标准技能工具
type SearchVideoTool struct {
	embedder *embedding.EmbeddingProvider
	vectorDB *vectordb.VectorDBProvider
}

// NewSearchVideoTool 初始化 RAG 技能
func NewSearchVideoTool(e *embedding.EmbeddingProvider, v *vectordb.VectorDBProvider) *SearchVideoTool {
	return &SearchVideoTool{
		embedder: e,
		vectorDB: v,
	}
}

// Name 必须返回工具的英文名，供大模型在 JSON 中指定调用
func (t *SearchVideoTool) Name() string {
	return "search_video_knowledge"
}

// Description 极其重要！这是给大模型看的说明书，决定了它在什么情况下会触发这个技能
func (t *SearchVideoTool) Description() string {
	return `当用户询问关于视频的具体内容、画面细节、对话片段、或者需要从视频库中寻找特定信息时，必须调用此工具。
请不要根据常识自己编造视频内容。
输入参数应当是你从用户问题中提炼出的核心搜索关键词（例如用户问“有没有讲大语言模型发展史的视频”，输入应为“大语言模型发展史”）。`
}

// Call 核心逻辑：大模型决定使用本工具后，LangChainGo 底层会自动执行这个函数
func (t *SearchVideoTool) Call(ctx context.Context, input string) (string, error) {
	// input 是大模型提取并传递过来的搜索词
	// 注意：有时大模型可能会传带引号的字符串，我们可以做一个简单的清理
	query := strings.Trim(input, `"'`)
	fmt.Printf("🛠️ [Skill 触发] 大模型正在使用向量检索，搜索词: %s\n", query)

	// 1. 调用 Embedding 将大模型的关键词向量化
	vector, err := t.embedder.EmbedText(ctx, query)
	if err != nil {
		return "", fmt.Errorf("生成搜索向量失败: %w", err)
	}

	// 2. 调用 VectorDB 进行相似度检索 (我们设定拿 Top-3 结果)
	chunks, err := t.vectorDB.SearchContext(ctx, vector, 3)
	if err != nil {
		return "", fmt.Errorf("数据库检索失败: %w", err)
	}

	// 3. 格式化检索结果给大模型
	if len(chunks) == 0 {
		return "未在视频库中找到相关的知识片段，请根据你的常识回答，并告知用户没有相关视频。", nil
	}

	var sb strings.Builder
	sb.WriteString("以下是从数据库中检索到的相关视频片段：\n")
	for _, chunk := range chunks {
		// 将后端的毫秒转为前端/用户可读的秒数
		seconds := float64(chunk.StartTime) / 1000.0
		sb.WriteString(fmt.Sprintf("- [视频ID:%d, 时间点:%.1fs]: %s\n", chunk.VideoID, seconds, chunk.Content))
	}
	// 把拼接好的事实数据返回给大模型
	return sb.String(), nil
}
