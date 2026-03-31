package vectordb

import (
	"context"
	"feedsystem_video_go/internal/config"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/llms/ollama"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/vectorstores"
)

const (
	// 默认的 Qdrant 集合名称
	defaultCollection = "video_chunks"
)

type VideoChunkMetadata struct {
	VideoID    uint   // 视频ID
	ChunkIndex int    // 切片序号
	ChunkType  string // asr_clip 或 ocr_clip
	StartTime  uint64 // 开始时间 (毫秒)
	EndTime    uint64 // 结束时间 (毫秒)
	Content    string // 切片文本内容 (冗余存储，方便检索后直接使用)
}

// ToMap 将结构体安全地转换为 LangChain 需要的 map
func (m VideoChunkMetadata) ToMap() map[string]any {
	return map[string]any{
		"video_id":    m.VideoID,
		"chunk_index": m.ChunkIndex,
		"chunk_type":  m.ChunkType,
		"start_time":  m.StartTime,
		"end_time":    m.EndTime,
		"content":     m.Content,
	}
}

// VectorDBProvider 负责与 Qdrant 交互
type VectorDBProvider struct {
	client     *qdrant.Client
	collection string
	embedder   embeddings.Embedder // 新增：必须绑定一个 Embedding 模型才能实现 LangChain 接口
}

var _ vectorstores.VectorStore = &VectorDBProvider{}

// NewVectorDBProvider 初始化 Qdrant 客户端
func NewVectorDBProvider(cfg config.VectorDBConfig, embCfg config.EmbeddingConfig) (*VectorDBProvider, error) {
	qClient, err := qdrant.NewClient(&qdrant.Config{
		Host: cfg.Host,
		Port: cfg.Port,
	})
	if err != nil {
		return nil, fmt.Errorf("connect qdrant failed: %w", err)
	}

	// ==================== 1. 自动建表逻辑 ====================
	// 尝试创建集合 (如果集合已经存在，Qdrant 会返回错误，我们可以忽略该错误)
	err = qClient.CreateCollection(context.Background(), &qdrant.CreateCollection{
		CollectionName: defaultCollection,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     1024,                   // ⚠️注意：这里填你实际使用的 Embedding 模型维度 (比如有些模型是 768 或 1536)
			Distance: qdrant.Distance_Cosine, // 默认使用余弦相似度
		}),
	})
	if err != nil {
		// 【核心修复】：判断是不是“already exists”
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			fmt.Printf("ℹ️ [Qdrant] 集合 %s 已存在，跳过创建\n", defaultCollection)
		} else {
			// 如果是超时、连不上等真正致命的错误，必须立刻终止并返回！
			return nil, fmt.Errorf("致命错误：连接 Qdrant 或创建集合失败: %w", err)
		}
	} else {
		fmt.Printf("✅ [Qdrant] 成功创建全新向量集合: %s\n", defaultCollection)
	}

	// ==================== 2. 自动建索引逻辑 ====================
	// 为 video_id 创建标量索引，加速删除和按视频过滤搜索的速度
	_, err = qClient.CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
		CollectionName: defaultCollection,
		FieldName:      "video_id",
		FieldType:      qdrant.FieldType_FieldTypeInteger.Enum(), // 因为你之前说改成整型了，这里用 Integer
	})
	if err != nil {
		fmt.Printf("ℹ️ [Qdrant] CreateFieldIndex stat (can ignore if already exists): %v\n", err)
	} else {
		fmt.Printf("✅ [Qdrant] 成功为 %s 创建 video_id 整型索引\n", defaultCollection)
	}

	opts := []ollama.Option{
		ollama.WithModel(embCfg.Model),
		ollama.WithServerURL(embCfg.Addr),
	}
	llm, err := ollama.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("init ollama client for embedding failed: %w", err)
	}

	// 2. 将普通的 LLM 包装成专门的 Embedder (提取特征向量)
	embedder, err := embeddings.NewEmbedder(llm)

	return &VectorDBProvider{
		client:     qClient,
		collection: defaultCollection,
		embedder:   embedder,
	}, nil
}

// 1. AddDocuments 实现了批量向量化 + 批量 gRPC 入库
func (p *VectorDBProvider) AddDocuments(ctx context.Context, docs []schema.Document, options ...vectorstores.Option) ([]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	// 1. 提取所有纯文本准备批量 Embedding
	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.PageContent
	}

	// 2. 调用大模型批量生成向量
	vectors, err := p.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("batch embed failed: %w", err)
	}

	// 3. 组装 gRPC 的 PointStruct
	points := make([]*qdrant.PointStruct, len(docs))
	returnedIDs := make([]string, len(docs))

	for i, doc := range docs {
		// 生成 UUID
		pointID := uuid.New().String()
		returnedIDs[i] = pointID
		points[i] = &qdrant.PointStruct{
			Id:      qdrant.NewIDUUID(pointID),
			Vectors: qdrant.NewVectors(vectors[i]...),
			Payload: qdrant.NewValueMap(doc.Metadata), // 自动将 Go map 转换
		}
	}

	// 4. 执行极速 gRPC 批量 Upsert
	_, err = p.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: p.collection,
		Points:         points,
		Wait:           qdrant.PtrOf(true),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant batch upsert failed: %w", err)
	}

	return returnedIDs, nil
}

// 2. SimilaritySearch 实现了将用户提问向量化，并搜索相关片段
func (p *VectorDBProvider) SimilaritySearch(ctx context.Context, query string, numDocuments int, options ...vectorstores.Option) ([]schema.Document, error) {
	// 1. 将用户的搜索词 (如: "怎么切肉") 转化为向量
	queryVectors, err := p.embedder.EmbedDocuments(ctx, []string{query})
	if err != nil || len(queryVectors) == 0 {
		return nil, fmt.Errorf("embed query failed: %w", err)
	}

	// 2. 发起 gRPC 检索
	searchRes, err := p.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: p.collection,
		Query:          qdrant.NewQuery(queryVectors[0]...),
		Limit:          qdrant.PtrOf(uint64(numDocuments)),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant search failed: %w", err)
	}

	// 3. 将 Qdrant 返回的底层数据，转换回 LangChain 标准的 Document
	var docs []schema.Document
	for _, hit := range searchRes {
		// 将 qdrant 的 payload 转回常规 map
		metadata := make(map[string]any)
		for k, v := range hit.Payload {
			// 简易类型转换，实际可能需要更细致的判断
			switch v.Kind.(type) {
			case *qdrant.Value_IntegerValue:
				metadata[k] = v.GetIntegerValue()
			case *qdrant.Value_DoubleValue:
				metadata[k] = v.GetDoubleValue()
			case *qdrant.Value_StringValue:
				metadata[k] = v.GetStringValue()
			}
		}

		docs = append(docs, schema.Document{
			PageContent: metadata["content"].(string), // 假设存的时候字段叫 content
			Metadata:    metadata,
			Score:       float32(hit.Score),
		})
	}

	return docs, nil
}

// Close 关闭数据库连接 (建议在 main 函数的 defer 中调用)
func (p *VectorDBProvider) Close() {
	if p.client != nil {
		p.client.Close()
	}
}

// DeleteVideoChunks 根据视频 ID 删除该视频对应的所有向量切片
// 它通过匹配 Payload 中的 "video_id" 字段来实现批量删除，无需预先知道具体的 Point UUID
func (p *VectorDBProvider) DeleteVideoChunks(ctx context.Context, videoID uint) error {
	// 1. 构建过滤器：要求 Payload 中的 "video_id" 必须等于传入的 videoID
	matchFilter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "video_id", // 对应你 Upsert 时存入 payload 的 key
						Match: &qdrant.Match{
							// 👇 核心修改点：换成 Match_Integer，并把 videoID 转为 int64
							MatchValue: &qdrant.Match_Integer{
								Integer: int64(videoID),
							},
						},
					},
				},
			},
		},
	}

	// 2. 调用 Qdrant 的 Delete 接口，传入刚刚构建的过滤器
	_, err := p.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: p.collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: matchFilter,
			},
		},
		// Wait 设置为 true，表示同步等待删除完成。
		// 这样可以确保接口返回后，这些数据是真的在库里消失了，防止大模型搜到“幽灵数据”
		Wait: qdrant.PtrOf(true),
	})

	if err != nil {
		return fmt.Errorf("qdrant delete chunks by video_id failed: %w", err)
	}

	return nil
}
