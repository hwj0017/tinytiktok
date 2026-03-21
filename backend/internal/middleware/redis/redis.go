package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"feedsystem_video_go/internal/config"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type Client struct {
	Rdb *redis.Client
}

func NewFromEnv(cfg *config.RedisConfig) (*Client, error) {
	Rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	return &Client{Rdb: Rdb}, nil
}

func (c *Client) Close() error {
	if c == nil || c.Rdb == nil {
		return nil
	}
	return c.Rdb.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.Rdb == nil {
		return nil
	}
	return c.Rdb.Ping(ctx).Err()
}

func IsMiss(err error) bool {
	return err == redis.Nil
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (c *Client) Lock(ctx context.Context, key string, ttl time.Duration) (token string, ok bool, err error) {
	if c == nil || c.Rdb == nil {
		return "", false, nil
	}
	token, err = randToken(16)
	if err != nil {
		return "", false, err
	}
	ok, err = c.Rdb.SetNX(ctx, key, token, ttl).Result()
	return token, ok, err
}

var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end
`)

func (c *Client) Unlock(ctx context.Context, key string, token string) error {
	if c == nil || c.Rdb == nil {
		return nil
	}
	_, err := unlockScript.Run(ctx, c.Rdb, []string{key}, token).Result()
	return err
}
