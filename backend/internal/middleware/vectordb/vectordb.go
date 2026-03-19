package vectordb

import (
	"context"
	"feedsystem_video_go/internal/config"
	"fmt"
	"strings"

	"github.com/qdrant/go-client/qdrant"
)

// VideoChunk 表示从向量数据库中检索出的视频片段元数据
type VideoChunk struct {
	VideoID   uint    // 视频 ID，方便后续去数据库查详情
	StartTime uint64  // 毫秒
	EndTime   uint64  // 毫秒
	Content   string  // 字幕文本
	Score     float32 // 相似度打分
}

// VectorDBProvider 负责与 Qdrant 交互
type VectorDBProvider struct {
	client     *qdrant.Client
	collection string
}

// NewVectorDBProvider 初始化 Qdrant 客户端
func NewVectorDBProvider(cfg config.VectorDBConfig) (*VectorDBProvider, error) {
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
		CollectionName: cfg.Collection,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     1024,                   // ⚠️注意：这里填你实际使用的 Embedding 模型维度 (比如有些模型是 768 或 1536)
			Distance: qdrant.Distance_Cosine, // 默认使用余弦相似度
		}),
	})
	if err != nil {
		// 【核心修复】：判断是不是“already exists”
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			fmt.Printf("ℹ️ [Qdrant] 集合 %s 已存在，跳过创建\n", cfg.Collection)
		} else {
			// 如果是超时、连不上等真正致命的错误，必须立刻终止并返回！
			return nil, fmt.Errorf("致命错误：连接 Qdrant 或创建集合失败: %w", err)
		}
	} else {
		fmt.Printf("✅ [Qdrant] 成功创建全新向量集合: %s\n", cfg.Collection)
	}

	// ==================== 2. 自动建索引逻辑 ====================
	// 为 video_id 创建标量索引，加速删除和按视频过滤搜索的速度
	_, err = qClient.CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
		CollectionName: cfg.Collection,
		FieldName:      "video_id",
		FieldType:      qdrant.FieldType_FieldTypeInteger.Enum(), // 因为你之前说改成整型了，这里用 Integer
	})
	if err != nil {
		fmt.Printf("ℹ️ [Qdrant] CreateFieldIndex stat (can ignore if already exists): %v\n", err)
	} else {
		fmt.Printf("✅ [Qdrant] 成功为 %s 创建 video_id 整型索引\n", cfg.Collection)
	}

	return &VectorDBProvider{
		client:     qClient,
		collection: cfg.Collection,
	}, nil
}

// SearchContext 拿着向量去数据库里搜索最相关的视频片段
func (p *VectorDBProvider) SearchContext(ctx context.Context, vector []float32, limit int) ([]VideoChunk, error) {
	// 调用 Qdrant SDK 进行查询
	searchRes, err := p.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: p.collection,
		Query:          qdrant.NewQuery(vector...),  // 传入用户的 1024 维向量
		Limit:          qdrant.PtrOf(uint64(limit)), // 限制返回条数 (Top-K)
		WithPayload:    qdrant.NewWithPayload(true), // 必须为 true 才能带回业务数据
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant search failed: %w", err)
	}

	// 解析 Qdrant 的 Payload 为我们的 Go 结构体
	var chunks []VideoChunk
	for _, hit := range searchRes {
		payload := hit.Payload

		chunk := VideoChunk{
			VideoID: uint(payload["video_id"].GetIntegerValue()),
			// Qdrant 中存的是 Double，我们转回后端的 uint64 毫秒格式
			StartTime: uint64(payload["start_time"].GetDoubleValue()),
			EndTime:   uint64(payload["end_time"].GetDoubleValue()),
			Content:   payload["text"].GetStringValue(),
			Score:     hit.Score,
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// Upsert 将向量和业务元数据插入或更新到 Qdrant 中
// 注意：Qdrant 的 pointID 必须是 uint64 或 合法的 UUID 字符串。这里我们采用 uint64。
func (p *VectorDBProvider) Upsert(ctx context.Context, pointID string, vector []float32, payload map[string]any) error {
	// 构建要插入的数据点
	point := &qdrant.PointStruct{
		// 1. 设置 ID (使用 uint64 完美契合大部分关系型数据库的主键)
		Id: qdrant.NewIDUUID(pointID),

		// 2. 设置向量
		Vectors: qdrant.NewVectors(vector...),

		// 3. 设置 Payload (元数据)。qdrant.NewValueMap 会自动将 Go 的 map 转换为 Qdrant 底层的 gRPC 类型
		Payload: qdrant.NewValueMap(payload),
	}

	// 执行 Upsert 操作
	_, err := p.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: p.collection,
		Points:         []*qdrant.PointStruct{point},
		// Wait 设置为 true，表示同步等待写入完成。
		// 这样可以确保这个请求返回后，立马去 Search 就能搜到这条数据（非常适合 RAG 流程）
		Wait: qdrant.PtrOf(true),
	})

	if err != nil {
		return fmt.Errorf("qdrant upsert failed: %w", err)
	}

	return nil
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
