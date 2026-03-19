package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"feedsystem_video_go/internal/config" // 假设你的 config 结构体存在于此处
)

// ASRProvider 负责将音频文件转换为文本
type ASRProvider struct {
	client *http.Client
	apiURL string
}

// WhisperResponse 对应 webservice 返回的完整 JSON
type WhisperResponse struct {
	Text     string           `json:"text"`     // 完整文本
	Segments []WhisperSegment `json:"segments"` // 按停顿切分的片段
	Language string           `json:"language"` // 识别出的语言
}

// WhisperSegment 对应每个带有时间戳的音频片段
type WhisperSegment struct {
	Id    int     `json:"id"`
	Start float64 `json:"start"` // 开始时间 (单位: 秒, 例如 2.56)
	End   float64 `json:"end"`   // 结束时间 (单位: 秒, 例如 5.12)
	Text  string  `json:"text"`  // 这几秒内的文本
}

// NewASRProvider 初始化语音识别服务
func NewASRProvider(cfg config.AsrConfig) (*ASRProvider, error) {
	// 1. 初始化 HTTP 客户端 (设置合理的超时时间，因为 Whisper 处理长音频需要一定时间)
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	// 2. 组装请求地址，onerahmet/openai-whisper-asr-webservice 默认提供 /asr 端点
	if cfg.Addr == "" {
		return nil, fmt.Errorf("asr service address is required")
	}
	apiURL := fmt.Sprintf("%s/asr?encode=true&task=transcribe&language=zh&output=json", cfg.Addr) // 示例: http://127.0.0.1:9000/asr

	return &ASRProvider{
		client: client,
		apiURL: apiURL,
	}, nil
}

// RecognizeSpeech 将音频转换为文本 (对外调用的代码同样极其干净！)
func (p *ASRProvider) RecognizeSpeech(ctx context.Context, audioData []byte, filename string) (*WhisperResponse, error) {
	// 1. 内部处理：构建 multipart/form-data 格式的请求体 (对调用者完全隐藏这些脏活累活)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 添加音频文件字段 "audio_file"
	part, err := writer.CreateFormFile("audio_file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file failed: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(audioData)); err != nil {
		return nil, fmt.Errorf("copy audio data failed: %w", err)
	}

	// 必须主动 Close，写入结尾的 boundary，否则服务端无法解析
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer failed: %w", err)
	}

	// 2. 创建带 Context 的 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, body)
	if err != nil {
		return nil, fmt.Errorf("create asr request failed: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// 3. 发送请求并获取结果
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do asr request failed: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read asr response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asr service returned status %d: %s", resp.StatusCode, string(respBytes))
	}
	// 【修改点 2】：解析 JSON 到结构体
	var whisperResp WhisperResponse
	if err := json.Unmarshal(respBytes, &whisperResp); err != nil {
		return nil, fmt.Errorf("unmarshal whisper json failed: %w", err)
	}

	// Whisper 服务返回的纯文本字符串，完美返回！
	return &whisperResp, nil
}
