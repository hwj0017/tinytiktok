package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/qdrant/go-client/qdrant"
	"net/http"
	"time"
)

// OllamaEmbedRequest 对应 Ollama /api/embed 的请求体
type OllamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// OllamaEmbedResponse 对应 Ollama /api/embed 的响应体
type OllamaEmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"` // 注意：Ollama 返回的是嵌套数组
}

type AgentService struct {
	QdrantClient *qdrant.Client
}

// SearchVideoKnowledge 作为 Skill 的底层实现
func (s *AgentService) SearchVideoKnowledge(ctx context.Context, query string) (string, error) {
	// 1. 获取向量 (假设已经有了 embedding 逻辑)
	vector, _ := s.getEmbedding(ctx, query)

	// 2. 检索 Qdrant
	searchResult, err := s.QdrantClient.Query(ctx, &qdrant.QueryPoints{
		CollectionName: "video_rag_chunks",
		Query:          qdrant.NewQuery(vector...),
		Limit:          qdrant.PtrOf(uint64(3)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return "", err
	}

	// 3. 格式化输出
	var contextText string
	for _, hit := range searchResult {
		p := hit.Payload

		// 从 Qdrant 取出的是 float64，转换回 uint64 毫秒
		startMs := uint64(p["start_time"].GetDoubleValue())

		// 转换成人类易读的格式，例如 12500ms -> 12.5s
		seconds := float64(startMs) / 1000.0

		contextText += fmt.Sprintf("[视频:%s, 时间:%.1fs]: %s\n",
			p["video_id"].GetStringValue(),
			seconds,
			p["text"].GetStringValue())
	}

	return contextText, nil
}

// getEmbedding 调用本地 Ollama 服务将文本转换为向量
func (s *AgentService) getEmbedding(ctx context.Context, text string) ([]float32, error) {
	// 1. 构造请求体
	reqBody := OllamaEmbedRequest{
		Model: "bge-m3", // 确保你之前已经 ollama pull bge-m3
		Input: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 2. 创建 HTTP 请求 (带 Context 以后端最佳实践防止超时)
	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/embed", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 3. 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Ollama 失败: %w", err)
	}
	defer resp.Body.Close()

	// 4. 检查状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama 返回错误状态码: %d", resp.StatusCode)
	}

	// 5. 解析响应
	var embedResp OllamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("解析响应 JSON 失败: %w", err)
	}

	// 6. 安全检查：确保返回了向量数据
	if len(embedResp.Embeddings) == 0 || len(embedResp.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("Ollama 未返回任何向量数据")
	}

	// 返回第一个输入对应的向量
	return embedResp.Embeddings[0], nil
}
