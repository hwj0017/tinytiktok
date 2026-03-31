package rabbitmq

import (
	"context"
	"errors"
)

// VideoMQ 负责分发视频处理任务
type VideoMQ struct {
	*RabbitMQ
}

const (
	// 1. 统一的交换机
	VideoExchange = "video.exchange"

	// 2. 路由键 (Routing Key)
	RKVideoCreated = "video.event.created"
	RKVideoDeleted = "video.event.deleted"

	// 3. 队列名 (Queue)
	QueueVideoProcess = "video.process.queue" // 监听 created，处理 AI 任务
	QueueVideoCleanup = "video.cleanup.queue" // 监听 deleted，处理删库任务
)

// ==========================================
// 核心改造：拆分为两个完全独立的 Event 结构体
// ==========================================

// VideoCreatedEvent 视频上传成功事件 (载荷丰富)
type VideoCreatedEvent struct {
	VideoID     uint     `json:"video_id"`
	VloggerID   uint     `json:"vlogger_id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Duration    int      `json:"duration"`
	CreateTime  int64    `json:"create_time"`
}

// VideoDeletedEvent 视频删除事件 (载荷极简)
type VideoDeletedEvent struct {
	VideoID   uint `json:"video_id"`
	VloggerID uint `json:"vlogger_id"` // 建议带上作者ID，方便消费端做权限二次校验或数据统计
}

// NewVideoMQ 初始化交换机和队列绑定
func NewVideoMQ(base *RabbitMQ) (*VideoMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}

	// 声明处理队列，绑定 Created 路由键
	if err := base.DeclareTopic(VideoExchange, QueueVideoProcess, RKVideoCreated); err != nil {
		return nil, err
	}

	// 声明清理队列，绑定 Deleted 路由键
	if err := base.DeclareTopic(VideoExchange, QueueVideoCleanup, RKVideoDeleted); err != nil {
		return nil, err
	}

	return &VideoMQ{RabbitMQ: base}, nil
}

// ==========================================
// 专属的发布方法，消灭 if-else 和 switch
// ==========================================

// PublishVideoCreated 发送视频创建事件
func (r *VideoMQ) PublishVideoCreated(ctx context.Context, evt VideoCreatedEvent) error {
	if r == nil || r.RabbitMQ == nil {
		return errors.New("video mq is not initialized")
	}
	// 直接发往 created 路由
	return r.PublishJSON(ctx, VideoExchange, RKVideoCreated, evt)
}

// PublishVideoDeleted 发送视频删除事件
func (r *VideoMQ) PublishVideoDeleted(ctx context.Context, evt VideoDeletedEvent) error {
	if r == nil || r.RabbitMQ == nil {
		return errors.New("video mq is not initialized")
	}
	// 直接发往 deleted 路由
	return r.PublishJSON(ctx, VideoExchange, RKVideoDeleted, evt)
}
