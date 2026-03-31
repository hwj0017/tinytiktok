package main

import (
	"context"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	apphttp "feedsystem_video_go/internal/http"
	"feedsystem_video_go/internal/middleware/elasticsearch"
	"feedsystem_video_go/internal/middleware/llm"
	rabbitmq "feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/middleware/vectordb"

	"log"
	"strconv"
	"time"
)

func main() {
	// 加载配置
	log.Printf("Loading config from configs/config.yaml")
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 连接数据库
	//log.Printf("Database config: %v", cfg.Database)
	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	if err := db.AutoMigrate(sqlDB); err != nil {
		log.Fatalf("Failed to auto migrate database: %v", err)
	}
	defer db.CloseDB(sqlDB)

	// 连接 Redis (可选，用于缓存)
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		log.Printf("Redis config error (cache disabled): %v", err)
		cache = nil
	} else {
		pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(pingCtx); err != nil {
			log.Printf("Redis not available (cache disabled): %v", err)
			_ = cache.Close()
			cache = nil
		} else {
			defer cache.Close()
			log.Printf("Redis connected (cache enabled)")
		}
	}

	// 连接 RabbitMQ (可选，用于消息队列)
	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		log.Printf("RabbitMQ config error (disabled): %v", err)
		rmq = nil
	} else {
		defer rmq.Close()
		log.Printf("RabbitMQ connected")
	}

	vdb, err := vectordb.NewVectorDBProvider(cfg.VectorDB, cfg.Embedding)
	if err != nil {
		log.Printf("VectorDB provider error (disabled): %v", err)
		vdb = nil
	} else {
		log.Printf("VectorDB provider connected")
	}
	llm, err := llm.NewLLM(cfg.LLM)
	if err != nil {
		log.Printf("LLM provider error (disabled): %v", err)
		llm = nil
	} else {
		log.Printf("LLM provider connected")
	}
	esClient, err := elasticsearch.NewClient(cfg.Elasticsearch)
	if err != nil {
		log.Printf("Elasticsearch client error (disabled): %v", err)
		esClient = nil
	} else {
		log.Printf("Elasticsearch client connected")
	}
	// 设置路由
	r := apphttp.SetRouter(sqlDB, cache, rmq, vdb, llm, esClient)
	log.Printf("Server is running on port %d", cfg.Server.Port)
	if err := r.Run(":" + strconv.Itoa(cfg.Server.Port)); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
