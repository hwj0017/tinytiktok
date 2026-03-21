package redis

import (
	"context"
	"time"
)

func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	return c.Rdb.Get(ctx, key).Bytes()
}

func (c *Client) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.Rdb.Set(ctx, key, value, ttl).Err()
}

func (c *Client) Del(ctx context.Context, key string) error {
	return c.Rdb.Del(ctx, key).Err()
}

// CF.EXISTS key value
func (c *Client) IsEXIST(ctx context.Context, key string, value string) (bool, error) {
	exist, err := c.Rdb.Do(ctx, "CF.EXISTS", key, value).Bool()
	return exist, err
}
func (c *Client) AddCF(ctx context.Context, key string, value string) error {
	return c.Rdb.Do(ctx, "CF.ADD", key, value).Err()
}
func (c *Client) DelCF(ctx context.Context, key string, value string) error {
	return c.Rdb.Do(ctx, "CF.DEL", key, value).Err()
}
