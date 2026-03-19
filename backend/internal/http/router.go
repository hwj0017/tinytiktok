package http

import (
	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/agent"
	"feedsystem_video_go/internal/agent/skill"

	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/middleware/embedding"
	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/middleware/vectordb"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
	"gorm.io/gorm"
)

func SetRouter(db *gorm.DB, cache *rediscache.Client, rmq *rabbitmq.RabbitMQ, emb *embedding.EmbeddingProvider, vectordb *vectordb.VectorDBProvider, llm llms.Model) *gin.Engine {
	r := gin.Default()
	r.Static("/static", "./.run/uploads")
	// account
	accountRepository := account.NewAccountRepository(db)
	accountService := account.NewAccountService(accountRepository, cache)
	accountHandler := account.NewAccountHandler(accountService)
	accountGroup := r.Group("/account")
	{
		accountGroup.POST("/register", accountHandler.CreateAccount)
		accountGroup.POST("/login", accountHandler.Login)
		accountGroup.POST("/changePassword", accountHandler.ChangePassword)
		accountGroup.POST("/findByID", accountHandler.FindByID)
		accountGroup.POST("/findByUsername", accountHandler.FindByUsername)
	}
	protectedAccountGroup := accountGroup.Group("")
	protectedAccountGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedAccountGroup.POST("/logout", accountHandler.Logout)
		protectedAccountGroup.POST("/rename", accountHandler.Rename)
	}
	// video
	videoRepository := video.NewVideoRepository(db)
	popularityMQ, err := rabbitmq.NewPopularityMQ(rmq)
	if err != nil {
		log.Printf("PopularityMQ init failed (mq disabled): %v", err)
		popularityMQ = nil
	}
	ragMQ, err := rabbitmq.NewRagMQ(rmq)
	if err != nil {
		log.Printf("RagMQ init failed (mq disabled): %v", err)
		ragMQ = nil
	}
	videoService := video.NewVideoService(videoRepository, cache, popularityMQ, ragMQ)
	videoHandler := video.NewVideoHandler(videoService, accountService)
	videoGroup := r.Group("/video")
	{
		videoGroup.POST("/listByAuthorID", videoHandler.ListByAuthorID)
		videoGroup.POST("/getDetail", videoHandler.GetDetail)
	}
	protectedVideoGroup := videoGroup.Group("")
	protectedVideoGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedVideoGroup.POST("/uploadVideo", videoHandler.UploadVideo)
		protectedVideoGroup.POST("/uploadCover", videoHandler.UploadCover)
		protectedVideoGroup.POST("/publish", videoHandler.PublishVideo)
		protectedVideoGroup.POST("/delete", videoHandler.DeleteVideo)
	}
	// like
	likeMQ, err := rabbitmq.NewLikeMQ(rmq)
	if err != nil {
		log.Printf("LikeMQ init failed (mq disabled): %v", err)
		likeMQ = nil
	}
	likeRepository := video.NewLikeRepository(db)
	likeService := video.NewLikeService(likeRepository, videoRepository, cache, likeMQ, popularityMQ)
	likeHandler := video.NewLikeHandler(likeService)
	likeGroup := r.Group("/like")
	protectedLikeGroup := likeGroup.Group("")
	protectedLikeGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedLikeGroup.POST("/like", likeHandler.Like)
		protectedLikeGroup.POST("/unlike", likeHandler.Unlike)
		protectedLikeGroup.POST("/isLiked", likeHandler.IsLiked)
		protectedLikeGroup.POST("/listMyLikedVideos", likeHandler.ListMyLikedVideos)
	}
	// comment
	commentRepository := video.NewCommentRepository(db)
	commentMQ, err := rabbitmq.NewCommentMQ(rmq)
	if err != nil {
		log.Printf("CommentMQ init failed (mq disabled): %v", err)
		commentMQ = nil
	}
	commentService := video.NewCommentService(commentRepository, videoRepository, cache, commentMQ, popularityMQ)
	commentHandler := video.NewCommentHandler(commentService, accountService)
	commentGroup := r.Group("/comment")
	{
		commentGroup.POST("/listAll", commentHandler.GetAllComments)
	}
	protectedCommentGroup := commentGroup.Group("")
	protectedCommentGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedCommentGroup.POST("/publish", commentHandler.PublishComment)
		protectedCommentGroup.POST("/delete", commentHandler.DeleteComment)
	}
	// social
	socialMQ, err := rabbitmq.NewSocialMQ(rmq)
	if err != nil {
		log.Printf("SocialMQ init failed (mq disabled): %v", err)
		socialMQ = nil
	}
	socialRepository := social.NewSocialRepository(db)
	socialService := social.NewSocialService(socialRepository, accountRepository, socialMQ)
	socialHandler := social.NewSocialHandler(socialService)
	socialGroup := r.Group("/social")
	protectedSocialGroup := socialGroup.Group("")
	protectedSocialGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedSocialGroup.POST("/follow", socialHandler.Follow)
		protectedSocialGroup.POST("/unfollow", socialHandler.Unfollow)
		protectedSocialGroup.POST("/getAllFollowers", socialHandler.GetAllFollowers)
		protectedSocialGroup.POST("/getAllVloggers", socialHandler.GetAllVloggers)
	}
	// feed
	feedRepository := feed.NewFeedRepository(db)
	feedService := feed.NewFeedService(feedRepository, likeRepository, cache)
	feedHandler := feed.NewFeedHandler(feedService)
	feedGroup := r.Group("/feed")
	feedGroup.Use(jwt.SoftJWTAuth(accountRepository, cache))
	{
		feedGroup.POST("/listLatest", feedHandler.ListLatest)
		feedGroup.POST("/listLikesCount", feedHandler.ListLikesCount)
		feedGroup.POST("/listByPopularity", feedHandler.ListByPopularity)
	}
	protectedFeedGroup := feedGroup.Group("")
	protectedFeedGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedFeedGroup.POST("/listByFollowing", feedHandler.ListByFollowing)
	}

	AIChatService := agent.NewAIChatService(llm, skill.NewSearchVideoTool(emb, vectordb), skill.NewSearchWeatherTool())
	AIChatHandler := agent.NewAIChatHandler(AIChatService)
	AIGroup := r.Group("/ai")
	AIGroup.POST("/chat", AIChatHandler.Chat)
	return r
}
