package elasticsearch

import (
	"feedsystem_video_go/internal/config"

	"github.com/elastic/go-elasticsearch/v8"
)

const (
	// ES 索引名称
	EsIndex = "videos"
)

type FullVideoEvent struct {
	VideoID     uint   `json:"video_id"`
	VideoURL    string `json:"video_url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ASRText     string `json:"asr_text"` // 语音识别出的文本
	OCRText     string `json:"ocr_text"` // 画面识别出的文本
}

func NewClient(cfg config.ElasticsearchConfig) (*elasticsearch.Client, error) {
	return elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{cfg.Url},
	})
}
