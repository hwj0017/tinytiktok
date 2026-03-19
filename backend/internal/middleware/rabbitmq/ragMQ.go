package rabbitmq

import (
	"context"
	"errors"
	"time"
)

// RagMQ 专门负责发送和处理 RAG 向量化任务的消息队列
type RagMQ struct {
	*RabbitMQ
}

const (
	// 定义 RAG 专属的 Exchange, Queue 和 Routing/Binding Key
	ragExchange   = "video.rag.events"
	ragQueue      = "video.rag.tasks" // 任务队列名
	ragBindingKey = "video.rag.*"

	// ragProcessRK 是发布“处理视频向量化任务”时的路由键
	ragProcessRK  = "video.rag.process"
	ActionPublish = "publish"
	ActionDelete  = "delete"
)

// RagTaskEvent 定义推送到消息队列的 RAG 任务数据结构
type RagTaskEvent struct {
	EventID    string    `json:"event_id"`
	VideoID    uint      `json:"video_id"` // 仅传递核心 ID，让消费者去数据库拉取详情
	Action     string    `json:"action"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewRagMQ 初始化 RAG 消息队列客户端并声明 Topic
func NewRagMQ(base *RabbitMQ) (*RagMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}

	// 声明 Topic 交换机并绑定 RAG 任务队列
	if err := base.DeclareTopic(ragExchange, ragQueue, ragBindingKey); err != nil {
		return nil, err
	}

	return &RagMQ{RabbitMQ: base}, nil
}

// PublishTask 发送视频入库（待向量化）任务到消息队列
func (r *RagMQ) PublishTask(ctx context.Context, videoID uint, action string) error {
	if r == nil || r.RabbitMQ == nil {
		return errors.New("rag mq is not initialized")
	}
	if videoID == 0 {
		return errors.New("videoID is required")
	}

	// 复用你们包内已有的 newEventID 函数生成唯一事件 ID
	id, err := newEventID(16)
	if err != nil {
		return err
	}

	// 组装事件消息
	event := RagTaskEvent{
		EventID:    id,
		VideoID:    videoID,
		Action:     action,
		OccurredAt: time.Now().UTC(),
	}

	// 序列化并发布 JSON 消息到 RabbitMQ
	return r.PublishJSON(ctx, ragExchange, ragProcessRK, event)
}
