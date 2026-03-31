package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"golang.org/x/sync/singleflight" // 需要引入此包
)

type VideoService struct {
	repo         *VideoRepository
	cache        *rediscache.Client
	cacheTTL     time.Duration
	popularityMQ *rabbitmq.PopularityMQ
	videoMQ      *rabbitmq.VideoMQ
	sf           singleflight.Group // 用于合并请求
}

func NewVideoService(repo *VideoRepository, cache *rediscache.Client, popularityMQ *rabbitmq.PopularityMQ, videoMQ *rabbitmq.VideoMQ) *VideoService {
	return &VideoService{repo: repo, cache: cache, cacheTTL: 5 * time.Minute, popularityMQ: popularityMQ, videoMQ: videoMQ}
}

func (vs *VideoService) Publish(ctx context.Context, video *Video) error {
	if video == nil {
		return errors.New("video is nil")
	}
	video.Title = strings.TrimSpace(video.Title)
	video.PlayURL = strings.TrimSpace(video.PlayURL)
	video.CoverURL = strings.TrimSpace(video.CoverURL)

	if video.Title == "" {
		return errors.New("title is required")
	}
	if video.PlayURL == "" {
		return errors.New("play url is required")
	}
	if video.CoverURL == "" {
		return errors.New("cover url is required")
	}
	if err := vs.repo.CreateVideo(ctx, video); err != nil {
		return err
	}
	if err := vs.videoMQ.PublishVideoCreated(ctx, rabbitmq.VideoCreatedEvent{
		VideoID:     video.ID,
		VloggerID:   video.AuthorID,
		URL:         video.PlayURL,
		Title:       video.Title,
		Description: video.Description,
	}); err != nil {
		return err
	}
	return nil
}

func (vs *VideoService) Delete(ctx context.Context, id uint, authorID uint) error {
	video, err := vs.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if video == nil {
		return errors.New("video not found")
	}
	if video.AuthorID != authorID {
		return errors.New("unauthorized")
	}
	if err := vs.repo.DeleteVideo(ctx, id); err != nil {
		return err
	}
	if err := vs.videoMQ.PublishVideoDeleted(ctx, rabbitmq.VideoDeletedEvent{
		VideoID:   video.ID,
		VloggerID: video.AuthorID,
	}); err != nil {
		return err
	}
	if vs.cache != nil {
		cacheKey := fmt.Sprintf("video:detail:id=%d", id)
		_ = vs.cache.Del(context.Background(), cacheKey)
	}
	return nil
}

func (vs *VideoService) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	videos, err := vs.repo.ListByAuthorID(ctx, int64(authorID))
	if err != nil {
		return nil, err
	}
	return videos, nil
}

func (vs *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {
	cacheKey := fmt.Sprintf("video:detail:id=%d", id)

	// 1. 尝试从缓存获取
	if vs.cache != nil {
		if v, ok := vs.getCached(ctx, cacheKey); ok {
			return v, nil
		}
	}

	// 2. 缓存缺失，使用 singleflight 合并并发请求
	// 返回值：v 为结果，err 为错误，shared 表示结果是否为多个调用者共享
	v, err, _ := vs.sf.Do(cacheKey, func() (interface{}, error) {
		// 【双重检查模式】进入 singleflight 后再查一次缓存，
		// 这样同一时刻进来的其他协程在第一个协程填补完缓存后，可以直接拿走结果。
		if vs.cache != nil {
			if cachedV, ok := vs.getCached(ctx, cacheKey); ok {
				return cachedV, nil
			}
		}

		// 3. 从数据库读取
		video, err := vs.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}

		// 4. 回填缓存
		if vs.cache != nil {
			vs.setCached(ctx, cacheKey, video)
		}

		return video, nil
	})

	if err != nil {
		return nil, err
	}

	return v.(*Video), nil
}

// 辅助函数：封装获取逻辑
func (vs *VideoService) getCached(ctx context.Context, key string) (*Video, bool) {
	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	b, err := vs.cache.GetBytes(opCtx, key)
	if err != nil {
		return nil, false
	}
	var cached Video
	if err := json.Unmarshal(b, &cached); err != nil {
		return nil, false
	}
	return &cached, true
}

// 辅助函数：封装设置逻辑
func (vs *VideoService) setCached(ctx context.Context, key string, video *Video) {
	b, err := json.Marshal(video)
	if err != nil {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_ = vs.cache.SetBytes(opCtx, key, b, vs.cacheTTL)
}

func (vs *VideoService) UpdateLikesCount(ctx context.Context, id uint, likesCount int64) error {
	if err := vs.repo.UpdateLikesCount(ctx, id, likesCount); err != nil {
		return err
	}
	return nil
}

func (vs *VideoService) UpdatePopularity(ctx context.Context, id uint, change int64) error {
	if err := vs.repo.UpdatePopularity(ctx, id, change); err != nil {
		return err
	}

	if vs.popularityMQ != nil {
		if err := vs.popularityMQ.Update(ctx, id, change); err == nil {
			return nil
		}
	}

	if vs.cache != nil {
		// 1) 详情缓存：直接失效（最简单靠谱）
		_ = vs.cache.Del(context.Background(), fmt.Sprintf("video:detail:id=%d", id))

		// 2) 热榜：写到“时间窗ZSET”，不要用 detail key
		now := time.Now().UTC().Truncate(time.Minute)
		windowKey := "hot:video:1m:" + now.Format("200601021504")
		member := strconv.FormatUint(uint64(id), 10)

		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		_ = vs.cache.ZincrBy(opCtx, windowKey, member, float64(change))
		_ = vs.cache.Expire(opCtx, windowKey, 2*time.Hour)
	}
	return nil
}
