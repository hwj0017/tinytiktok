package ocr

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

// OCRProvider 负责将单张图片/视频帧转换为文本
type OCRProvider struct {
	client *http.Client
	apiURL string
}

// OCRResponse 对应 FastAPI PaddleOCR 服务返回的完整 JSON
type OCRResponse struct {
	Code  int    `json:"code"`            // 业务状态码 (0 为成功)
	Text  string `json:"text"`            // 识别出的完整文本拼接
	Error string `json:"error,omitempty"` // 错误信息（仅在 Code 不为 0 时存在）
}

// NewOCRProvider 初始化画面文字识别服务
func NewOCRProvider(cfg config.OcrConfig) (*OCRProvider, error) {
	// 1. 初始化 HTTP 客户端
	// 注意：单张图片 OCR 通常在 0.5-2 秒内完成，这里设置 30 秒超时绰绰有余
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 2. 组装请求地址
	if cfg.Addr == "" {
		return nil, fmt.Errorf("ocr service address is required")
	}

	// 根据我们之前写的 FastAPI 路由，端点为 /ocr
	apiURL := fmt.Sprintf("%s/ocr", cfg.Addr) // 示例: http://127.0.0.1:8000/ocr

	return &OCRProvider{
		client: client,
		apiURL: apiURL,
	}, nil
}

// RecognizeImageText 将单张图片画面转换为文本 (对调用者完全隐藏 HTTP 构建细节)
func (p *OCRProvider) RecognizeImageText(ctx context.Context, imageData []byte, filename string) (string, error) {
	// 1. 内部处理：构建 multipart/form-data 格式的请求体
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 【关键映射】：添加图片文件字段 "image" (必须与 FastAPI 中定义的参数名一致)
	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		return "", fmt.Errorf("create form file failed: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(imageData)); err != nil {
		return "", fmt.Errorf("copy image data failed: %w", err)
	}

	// 必须主动 Close，写入结尾的 boundary，否则服务端无法解析
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer failed: %w", err)
	}

	// 2. 创建带 Context 的 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, body)
	if err != nil {
		return "", fmt.Errorf("create ocr request failed: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// 3. 发送请求并获取结果
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("do ocr request failed: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read ocr response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ocr service returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	// 4. 解析 JSON 到结构体
	var ocrResp OCRResponse
	if err := json.Unmarshal(respBytes, &ocrResp); err != nil {
		return "", fmt.Errorf("unmarshal ocr json failed: %w", err)
	}

	// 检查业务逻辑错误 (比如 Python 代码里抛出了异常)
	if ocrResp.Code != 0 {
		return "", fmt.Errorf("ocr service internal error: %s", ocrResp.Error)
	}

	return ocrResp.Text, nil
}
