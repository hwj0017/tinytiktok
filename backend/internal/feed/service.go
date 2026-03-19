package feed

import (
	"context"
	"encoding/json"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/video"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
)

type FeedService struct {
	repo     *FeedRepository
	likeRepo *video.LikeRepository
	cache    *rediscache.Client
	cacheTTL time.Duration
	sf       singleflight.Group
}

func NewFeedService(repo *FeedRepository, likeRepo *video.LikeRepository, cache *rediscache.Client) *FeedService {
	return &FeedService{repo: repo, likeRepo: likeRepo, cache: cache, cacheTTL: 5 * time.Second}
}

// 查询最新视频
func (f *FeedService) ListLatest(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {
	// 1. 定义从数据库加载数据的闭包 (封装原有逻辑)
	doListLatestFromDB := func() (ListLatestResponse, error) {
		videos, err := f.repo.ListLatest(ctx, limit, latestBefore)
		if err != nil {
			return ListLatestResponse{}, err
		}
		var nextTime int64
		if len(videos) > 0 {
			nextTime = videos[len(videos)-1].CreateTime.Unix()
		}
		hasMore := len(videos) == limit
		feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
		if err != nil {
			return ListLatestResponse{}, err
		}
		return ListLatestResponse{
			VideoList: feedVideos,
			NextTime:  nextTime,
			HasMore:   hasMore,
		}, nil
	}

	// 2. 如果是登录用户，不走缓存，直接查库 (保持原逻辑)
	if viewerAccountID != 0 || f.cache == nil {
		return doListLatestFromDB()
	}

	// 3. 生成缓存 Key
	before := int64(0)
	if !latestBefore.IsZero() {
		before = latestBefore.Unix()
	}
	cacheKey := fmt.Sprintf("feed:listLatest:limit=%d:before=%d", limit, before)

	// 4. 第一层检查：直接查缓存 (Fast Path)
	if resp, ok := f.getCached(ctx, cacheKey); ok {
		return resp, nil
	}

	// 5. 缓存未命中，使用 singleflight 合并请求
	val, err, _ := f.sf.Do(cacheKey, func() (interface{}, error) {
		// 【双重检查】进入 singleflight 后再查一次缓存
		// 解决“前一个班车刚走，缓存还没写完/读到”的极小概率并发问题
		if resp, ok := f.getCached(ctx, cacheKey); ok {
			return resp, nil
		}

		// 查数据库
		resp, err := doListLatestFromDB()
		if err != nil {
			return nil, err
		}

		// 回填缓存
		f.setCached(ctx, cacheKey, resp)

		return resp, nil
	})

	if err != nil {
		return ListLatestResponse{}, err
	}

	return val.(ListLatestResponse), nil
}

// --- 辅助方法，让主逻辑更干净 ---

func (f *FeedService) getCached(ctx context.Context, key string) (ListLatestResponse, bool) {
	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	b, err := f.cache.GetBytes(cacheCtx, key)
	if err != nil {
		return ListLatestResponse{}, false
	}
	var cached ListLatestResponse
	if err := json.Unmarshal(b, &cached); err == nil {
		return cached, true
	}
	return ListLatestResponse{}, false
}

func (f *FeedService) setCached(ctx context.Context, key string, resp ListLatestResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_ = f.cache.SetBytes(cacheCtx, key, b, f.cacheTTL)
}

// 按照点赞数查询视频
func (f *FeedService) ListLikesCount(ctx context.Context, limit int, cursor *LikesCountCursor, viewerAccountID uint) (ListLikesCountResponse, error) {
	videos, err := f.repo.ListLikesCountWithCursor(ctx, limit, cursor)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	hasMore := len(videos) == limit
	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	resp := ListLikesCountResponse{
		VideoList: feedVideos,
		HasMore:   hasMore,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextLikesCountBefore := last.LikesCount
		nextIDBefore := last.ID
		resp.NextLikesCountBefore = &nextLikesCountBefore
		resp.NextIDBefore = &nextIDBefore
	}
	return resp, nil
}

// 按照关注列表查询视频
func (f *FeedService) ListByFollowing(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListByFollowingResponse, error) {
	// 1. 如果没有登录，关注列表为空（逻辑兜底）
	if viewerAccountID == 0 {
		return ListByFollowingResponse{
			VideoList: make([]FeedVideoItem, 0),
			NextTime:  0,
			HasMore:   false,
		}, nil
	}

	// 2. 直接从数据库查询
	videos, err := f.repo.ListByFollowing(ctx, limit, viewerAccountID, latestBefore)
	if err != nil {
		return ListByFollowingResponse{}, err
	}

	// 3. 计算分页游标 NextTime
	var nextTime int64
	if len(videos) > 0 {
		nextTime = videos[len(videos)-1].CreateTime.Unix()
	} else {
		nextTime = 0
	}

	// 4. 判断是否还有更多
	hasMore := len(videos) == limit

	// 5. 组装聚合数据（如点赞状态、作者信息等）
	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByFollowingResponse{}, err
	}

	// 6. 构造返回结构
	return ListByFollowingResponse{
		VideoList: feedVideos,
		NextTime:  nextTime,
		HasMore:   hasMore,
	}, nil
}

func (f *FeedService) ListByPopularity(ctx context.Context, limit int, reqAsOf int64, offset int, viewerAccountID uint, latestPopularity int64, latestBefore time.Time, latestIDBefore uint) (ListByPopularityResponse, error) {
	// Redis 热榜（稳定分页：as_of + offset）
	if f.cache != nil {
		asOf := time.Now().UTC().Truncate(time.Minute)
		if reqAsOf > 0 {
			asOf = time.Unix(reqAsOf, 0).UTC().Truncate(time.Minute)
		}

		const win = 60
		keys := make([]string, 0, win)
		for i := 0; i < win; i++ {
			keys = append(keys, "hot:video:1m:"+asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504"))
		}

		dest := "hot:video:merge:1m:" + asOf.Format("200601021504") // 快照key：同一个as_of页内复用
		opCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		defer cancel()

		exists, _ := f.cache.Exists(opCtx, dest)
		if !exists {
			_ = f.cache.ZUnionStore(opCtx, dest, keys, "SUM")
			_ = f.cache.Expire(opCtx, dest, 2*time.Minute) // 给翻页留时间
		}

		start := int64(offset)
		stop := start + int64(limit) - 1
		members, err := f.cache.ZRevRange(opCtx, dest, start, stop)
		if err == nil && len(members) == 0 {
			if offset > 0 {
				return ListByPopularityResponse{
					VideoList:  []FeedVideoItem{},
					AsOf:       asOf.Unix(),
					NextOffset: offset,
					HasMore:    false,
				}, nil
			}
		}
		if err == nil && len(members) > 0 {
			ids := make([]uint, 0, len(members))
			for _, m := range members {
				u, err := strconv.ParseUint(m, 10, 64)
				if err == nil && u > 0 {
					ids = append(ids, uint(u))
				}
			}

			videos, err := f.repo.GetByIDs(ctx, ids)
			if err == nil {
				byID := make(map[uint]*video.Video, len(videos))
				for _, v := range videos {
					byID[v.ID] = v
				}
				ordered := make([]*video.Video, 0, len(ids))
				for _, id := range ids {
					if v := byID[id]; v != nil {
						ordered = append(ordered, v)
					}
				}
				items, err := f.buildFeedVideos(ctx, ordered, viewerAccountID)
				if err != nil {
					return ListByPopularityResponse{}, err
				}
				resp := ListByPopularityResponse{
					VideoList:  items,
					AsOf:       asOf.Unix(),
					NextOffset: offset + len(items),
					HasMore:    len(items) == limit,
				}
				if len(ordered) > 0 {
					last := ordered[len(ordered)-1]
					nextPopularity := last.Popularity
					nextBefore := last.CreateTime
					nextID := last.ID
					resp.NextLatestPopularity = &nextPopularity
					resp.NextLatestBefore = &nextBefore
					resp.NextLatestIDBefore = &nextID
				}
				return resp, nil
			}
		}
	}

	videos, err := f.repo.ListByPopularity(ctx, limit, latestPopularity, latestBefore, latestIDBefore)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	items, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	resp := ListByPopularityResponse{
		VideoList:  items,
		AsOf:       0,
		NextOffset: 0,
		HasMore:    len(items) == limit,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextPopularity := last.Popularity
		nextBefore := last.CreateTime
		nextID := last.ID
		resp.NextLatestPopularity = &nextPopularity
		resp.NextLatestBefore = &nextBefore
		resp.NextLatestIDBefore = &nextID
	}
	return resp, nil
}

func (f *FeedService) buildFeedVideos(ctx context.Context, videos []*video.Video, viewerAccountID uint) ([]FeedVideoItem, error) {
	feedVideos := make([]FeedVideoItem, 0, len(videos))
	videoIDs := make([]uint, len(videos))
	for i, v := range videos {
		videoIDs[i] = v.ID
	}
	likedMap, err := f.likeRepo.BatchGetLiked(ctx, videoIDs, viewerAccountID)
	if err != nil {
		return nil, err
	}
	for _, video := range videos {
		feedVideos = append(feedVideos, FeedVideoItem{
			ID:          video.ID,
			Author:      FeedAuthor{ID: video.AuthorID, Username: video.Username},
			Title:       video.Title,
			Description: video.Description,
			PlayURL:     video.PlayURL,
			CoverURL:    video.CoverURL,
			CreateTime:  video.CreateTime.Unix(),
			LikesCount:  video.LikesCount,
			IsLiked:     likedMap[video.ID],
		})
	}
	return feedVideos, nil
}
