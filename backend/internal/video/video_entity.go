package video

import (
	"time"

	"gorm.io/gorm"
)

// Video 对应 MySQL 中的 videos 表 (PO: Persistent Object)
type Video struct {
	// 1. 主键：改为 int64，并关闭 gorm 的自动自增。以便接收 API 层生成的雪花 ID。
	ID int64 `gorm:"primaryKey;autoIncrement:false" json:"id"`

	// 关联字段
	AuthorID int64 `gorm:"index;not null" json:"author_id"`
	// [已删除] Username: 用户名随时会改，不要冗余在视频表里，应通过 AuthorID 去缓存关联。

	// 核心业务字段
	Title       string `gorm:"type:varchar(255);not null" json:"title"`
	Description string `gorm:"type:varchar(255);" json:"description,omitempty"`
	PlayURL     string `gorm:"type:varchar(255);not null" json:"play_url"`
	CoverURL    string `gorm:"type:varchar(255);not null" json:"cover_url"`

	// 2. 统计字段 (反范式设计，由后端 Worker 异步更新)
	LikesCount    int64 `gorm:"not null;default:0" json:"likes_count"`
	CommentsCount int64 `gorm:"not null;default:0" json:"comments_count"`
	SharesCount   int64 `gorm:"not null;default:0" json:"shares_count"`
	// [已删除] Popularity: 坚决不存入数据库，热度分数仅存在 Redis 的 ZSet 中。

	// 3. 审计字段 (标准规范)
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"` // 软删除，查库时自动忽略，且不返回给前端
}

type PublishVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	PlayURL     string `json:"play_url"`
	CoverURL    string `json:"cover_url"`
}

type DeleteVideoRequest struct {
	ID uint `json:"id"`
}

type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id"`
}

type GetDetailRequest struct {
	ID uint `json:"id"`
}

type UpdateLikesCountRequest struct {
	ID         uint  `json:"id"`
	LikesCount int64 `json:"likes_count"`
}
