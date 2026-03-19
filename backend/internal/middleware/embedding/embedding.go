package embedding

import (
	"context"
	"feedsystem_video_go/internal/config"
	"fmt"

	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/llms/ollama"
)

// EmbeddingProvider 负责文本的向量化
type EmbeddingProvider struct {
	embedder *embeddings.EmbedderImpl
}

// NewEmbeddingProvider 使用 langchaingo SDK 初始化向量化服务
func NewEmbeddingProvider(cfg config.EmbeddingConfig) (*EmbeddingProvider, error) {
	// 1. 初始化 Ollama LLM 客户端 (和对话模型一模一样的操作)
	opts := []ollama.Option{
		ollama.WithModel(cfg.Model),
		ollama.WithServerURL(cfg.Addr),
	}
	llm, err := ollama.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("init ollama client for embedding failed: %w", err)
	}

	// 2. 将普通的 LLM 包装成专门的 Embedder (提取特征向量)
	embedder, err := embeddings.NewEmbedder(llm)
	if err != nil {
		return nil, fmt.Errorf("wrap embedder failed: %w", err)
	}

	return &EmbeddingProvider{
		embedder: embedder,
	}, nil
}

// EmbedText 将单个文本转换为向量 (代码瞬间变得极其干净！)
func (p *EmbeddingProvider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	// 直接调用 SDK 的 EmbedQuery，不需要再处理任何 HTTP JSON 序列化
	vector, err := p.embedder.EmbedQuery(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("sdk embed text failed: %w", err)
	}

	// langchaingo 返回的 vector 默认是 []float32，完美契合 Qdrant！
	return vector, nil
}
