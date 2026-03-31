package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"feedsystem_video_go/internal/middleware/asr"
	es "feedsystem_video_go/internal/middleware/elasticsearch"
	"feedsystem_video_go/internal/middleware/ocr"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	"feedsystem_video_go/internal/middleware/vectordb"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/tmc/langchaingo/schema"
)

// -----------------------------------------------------------------------------
// 1. 数据模型定义
// -----------------------------------------------------------------------------
const (
	// ES 索引名称
	esIndex = "videos"
)

// -----------------------------------------------------------------------------
// 2. Worker 结构体与初始化
// -----------------------------------------------------------------------------

type VideoWorker struct {
	ch          *amqp.Channel
	esClient    *elasticsearch.Client
	asr         *asr.ASRProvider
	ocr         *ocr.OCRProvider
	vectorStore *vectordb.VectorDBProvider
}

// NewVideoWorker 实例化一个新的视频处理消费者
func NewVideoWorker(ch *amqp.Channel, es *elasticsearch.Client, asr *asr.ASRProvider, ocr *ocr.OCRProvider, vectorStore *vectordb.VectorDBProvider) *VideoWorker {
	return &VideoWorker{
		ch:          ch,
		esClient:    es,
		asr:         asr,
		ocr:         ocr,
		vectorStore: vectorStore,
	}
}

// -----------------------------------------------------------------------------
// 3. 消费者生命周期管理 (Run & Handle)
// -----------------------------------------------------------------------------

// Run 启动消费者，开始监听队列
func (w *VideoWorker) Run(ctx context.Context) error {
	if w.ch == nil || w.esClient == nil {
		return errors.New("worker dependencies not initialized")
	}

	// 1. 获取上传处理队列的 Channel
	processDeliveries, err := w.ch.Consume(
		rabbitmq.QueueVideoProcess, "", false, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume process queue: %v", err)
	}

	// 2. 获取删除清理队列的 Channel
	cleanupDeliveries, err := w.ch.Consume(
		rabbitmq.QueueVideoCleanup, "", false, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume cleanup queue: %v", err)
	}

	log.Printf("🚀 VideoWorker is running, concurrently listening to [%s] and [%s]", rabbitmq.QueueVideoProcess, rabbitmq.QueueVideoCleanup)

	var wg sync.WaitGroup
	wg.Add(2)

	// 协程 A：专门处理极重的 AI 识别和入库 (Create)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Println("Process worker shutting down...")
				return
			case d, ok := <-processDeliveries:
				if !ok {
					return
				}
				w.handleProcessDelivery(ctx, d)
			}
		}
	}()

	// 协程 B：专门处理极轻的删库清理操作 (Delete)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Println("Cleanup worker shutting down...")
				return
			case d, ok := <-cleanupDeliveries:
				if !ok {
					return
				}
				w.handleCleanupDelivery(ctx, d)
			}
		}
	}()

	// 阻塞等待父 Context 取消
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

// handleDelivery 处理单条消息的 Ack/Nack 逻辑
// handleProcessDelivery 处理上传事件
func (w *VideoWorker) handleProcessDelivery(ctx context.Context, d amqp.Delivery) {
	var evt rabbitmq.VideoCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("⚠️ Invalid process json format, dropping message: %v", err)
		_ = d.Ack(false) // 垃圾数据直接丢弃
		return
	}

	log.Printf("⏳ Start processing video [%d]...", evt.VideoID)

	fullEvt := es.FullVideoEvent{VideoID: evt.VideoID, VideoURL: evt.URL, Title: evt.Title, Description: evt.Description}

	var wg sync.WaitGroup
	wg.Add(2)

	// 协程 1：执行语音识别 (ASR)
	go func() {
		defer wg.Done()
		text, err := w.runASR(ctx, evt.VideoID, evt.URL)
		if err != nil {
			log.Printf("⚠️ ASR Error [%s]: %v", evt, err)
			return // 即使报错也 return，不阻断整体流程
		}
		fullEvt.ASRText = text
	}()

	// 协程 2：执行画面文字识别 (OCR)
	go func() {
		defer wg.Done()
		text, err := w.runOCR(ctx, evt.VideoID, evt.URL)
		if err != nil {
			log.Printf("⚠️ OCR Error [%d]: %v", evt, err)
			return
		}
		fullEvt.OCRText = text
	}()

	// 等待两个识别任务全部完成
	wg.Wait()

	// 2. 存入 Elasticsearch
	if err := w.indexToES(ctx, fullEvt); err != nil {
		log.Printf("❌ ES Index Error for video [%d]: %v", evt.VideoID, err)
		_ = d.Nack(false, true) // 失败重试
		return
	}

	_ = d.Ack(false)
}

// handleCleanupDelivery 处理删除事件
func (w *VideoWorker) handleCleanupDelivery(ctx context.Context, d amqp.Delivery) {
	var evt rabbitmq.VideoDeletedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("⚠️ Invalid cleanup json format, dropping message: %v", err)
		_ = d.Ack(false)
		return
	}

	log.Printf("🗑️ Start cleaning up video [%d]...", evt.VideoID)

	// 1. 删向量库
	if err := w.vectorStore.DeleteVideoChunks(ctx, evt.VideoID); err != nil {
		log.Printf("❌ VectorDB Delete Error for video [%d]: %v", evt.VideoID, err)
		_ = d.Nack(false, true)
		return
	}

	// 2. 删 ES
	if err := w.removeFromES(ctx, evt.VideoID); err != nil {
		log.Printf("❌ ES Delete Error for video [%d]: %v", evt.VideoID, err)
		_ = d.Nack(false, true)
		return
	}

	_ = d.Ack(false)
}

// runASR 模拟语音识别逻辑
func (w *VideoWorker) runASR(ctx context.Context, videoId uint, url string) (string, error) {
	// 1. 提取音频
	audioData, err := ExtractAudio(ctx, url)
	if err != nil {
		return "", fmt.Errorf("extract audio failed: %w", err)
	}

	// 2. 语音识别 (Whisper)
	filename := fmt.Sprintf("video_%s.wav", videoId) // 建议 VideoID 统一为 string 或转为 string
	whisperData, err := w.asr.RecognizeSpeech(ctx, audioData, filename)
	if err != nil {
		return "", fmt.Errorf("asr recognition failed: %w", err)
	}
	if whisperData == nil || len(whisperData.Segments) == 0 {
		return "", nil
	}

	// ==========================================
	// 核心改造：组装批量入库队列
	// ==========================================
	windowSize := 6
	stepSize := 4
	var docs []schema.Document // 准备批量入库的 Document 数组
	chunkIndex := 0

	for i := 0; i < len(whisperData.Segments); i += stepSize {
		end := i + windowSize
		if end > len(whisperData.Segments) {
			end = len(whisperData.Segments)
		}

		startTime := whisperData.Segments[i].Start
		endTime := whisperData.Segments[end-1].End

		var sb strings.Builder
		for j := i; j < end; j++ {
			text := strings.TrimSpace(whisperData.Segments[j].Text)
			if text != "" {
				sb.WriteString(text + " ")
			}
		}

		mergedText := strings.TrimSpace(sb.String())

		// 忽略极短内容
		if len(mergedText) < 10 {
			if end == len(whisperData.Segments) {
				break
			}
			continue
		}

		// 🌟 仅仅是组装数据，不发起网络请求
		doc := schema.Document{
			PageContent: mergedText,
			Metadata: vectordb.VideoChunkMetadata{
				VideoID:    videoId,
				ChunkIndex: chunkIndex,
				ChunkType:  "asr_clip",               // 区分于 ocr_clip
				StartTime:  uint64(startTime * 1000), // 毫秒级
				EndTime:    uint64(endTime * 1000),
				Content:    mergedText,
			}.ToMap(),
		}
		docs = append(docs, doc)
		chunkIndex++

		if end == len(whisperData.Segments) {
			break
		}
	}

	// ==========================================
	// 3. 执行真正的批量入库 (一次性完成 Embedding + Upsert)
	// ==========================================
	if len(docs) > 0 {
		log.Printf("Rag worker: video_id=%d batch uploading %d chunks to VectorDB", videoId, len(docs))

		// AddDocuments 内部会自动分批请求 OpenAI Embedding 并批量存入 Qdrant
		_, err := w.vectorStore.AddDocuments(ctx, docs)
		if err != nil {
			// 记录错误但不中断流程，保证 ES 至少有数据
			log.Printf("⚠️ Batch upload to vectorDB failed: %v", err)
		} else {
			log.Printf("✅ Batch upload successful for video_id=%d", videoId)
		}
	}

	// 返回完整文本，供后续存入 Elasticsearch
	return whisperData.Text, nil
}

// runOCR 提取画面文字，返回合并的长文本(供ES使用)，并同时将带时间戳的切片存入向量库(供Agent精确空降)
func (w *VideoWorker) runOCR(ctx context.Context, videoID uint, url string) (string, error) {
	// 1. 创建临时目录... (与之前代码相同)
	tempDir, err := os.MkdirTemp("", "video_ocr_frames_*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 2. FFmpeg 抽帧 (fps=1)
	framePattern := filepath.Join(tempDir, "frame_%04d.jpg")
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", url, "-vf", "fps=1", "-q:v", "2", framePattern,
	)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg 抽帧失败: %v", err)
	}

	// 3. 读取图片文件
	// 注意：Glob 会自动按字母顺序排序 (frame_0001.jpg, frame_0002.jpg...)，这保证了时间轴的正确性！
	files, err := filepath.Glob(filepath.Join(tempDir, "frame_*.jpg"))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("未找到抽取的视频帧")
	}

	// 4. 遍历识别，并准备向量库入库
	var finalOCRText strings.Builder
	var lastText string
	var docs []schema.Document // 准备批量入库的 LangChain 文档切片

	// 使用下标 i 来推算时间 (因为 fps=1，i 的单位正好是秒)
	for i, file := range files {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		imageData, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		// 调用 OCR 微服务
		text, err := w.ocr.RecognizeImageText(ctx, imageData, filepath.Base(file))
		if err != nil {
			continue
		}

		text = strings.TrimSpace(text)
		// 如果画面里没字，或者字幕没变（去重），直接跳过
		if text == "" || text == lastText {
			continue
		}

		// --- 🌟 向量数据库的魔法在这里 🌟 ---
		// 一旦发现了新的画面文字，立刻把它包装成带有精准时间戳的切片
		doc := schema.Document{
			PageContent: text,
			Metadata: map[string]any{
				"video_id":   videoID,
				"chunk_type": "ocr_clip",             // 标记这是画面文字，与 asr_clip 区分开
				"start_time": uint64(i * 1000),       // 开始时间(毫秒)
				"end_time":   uint64((i + 1) * 1000), // 结束时间(毫秒)
			},
		}
		docs = append(docs, doc) // 塞进等待入库的数组
		// ------------------------------------

		// 拼接到最终结果里 (供存入 ES 兜底)
		finalOCRText.WriteString(text + " ")
		lastText = text
	}

	// 5. 批量将 OCR 切片存入 Qdrant 向量数据库
	if len(docs) > 0 {
		log.Printf("准备将 %d 个画面文字片段存入向量库...", len(docs))
		_, err := w.vectorStore.AddDocuments(ctx, docs)
		if err != nil {
			// 哪怕向量入库失败，我们也不 return error 中断整个流程
			// 至少把长文本返回去存进 ES
			log.Printf("⚠️ OCR 向量批量入库失败: %v", err)
		} else {
			log.Printf("✅ OCR 向量入库成功！")
		}
	}

	return strings.TrimSpace(finalOCRText.String()), nil
}

// -----------------------------------------------------------------------------
// 6. Elasticsearch 数据持久化层
// -----------------------------------------------------------------------------

// indexToES 执行文档的创建或全量覆盖更新
func (w *VideoWorker) indexToES(ctx context.Context, evt es.FullVideoEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal FullVideoEvent: %w", err)
	}

	req := esapi.IndexRequest{
		Index:      esIndex,
		DocumentID: fmt.Sprintf("%d", evt.VideoID),
		Body:       bytes.NewReader(data),
		Refresh:    "wait_for", // 阻塞直到文档可被搜索，适合开发环境；生产环境可视情况改为 "false"
	}

	res, err := req.Do(ctx, w.esClient)
	if err != nil {
		return fmt.Errorf("failed to execute ES request: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("ES index error [%d]: %s", res.StatusCode, res.String())
	}

	log.Printf("✅ Successfully indexed video [%d] to ES", evt.VideoID)
	return nil
}

// removeFromES 从 ES 中删除指定视频文档
func (w *VideoWorker) removeFromES(ctx context.Context, videoID uint) error {
	req := esapi.DeleteRequest{
		Index:      esIndex,
		DocumentID: fmt.Sprintf("%d", videoID),
	}

	res, err := req.Do(ctx, w.esClient)
	if err != nil {
		return fmt.Errorf("failed to execute ES delete request: %w", err)
	}
	defer res.Body.Close()

	// 如果文档本身不存在 (404)，不视为错误
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("ES delete error [%d]: %s", res.StatusCode, res.String())
	}

	log.Printf("✅ Successfully deleted video [%s] from ES", videoID)
	return nil
}

// ExtractAudio 使用 FFmpeg 从视频 URL 或本地路径中提取音频流
// 返回标准的 16kHz, 单声道, 16-bit PCM WAV 格式字节流 (这是 OpenAI Whisper 引擎最喜欢的完美格式)
func ExtractAudio(ctx context.Context, videoURL string) ([]byte, error) {
	// 准备两个内存缓冲区：一个接音频数据，一个接报错日志
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer

	// 构造 FFmpeg 命令
	// 注意最后的 "-"，它代表将生成的文件直接吐到标准输出 (stdout) 里，而不是存入硬盘
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoURL, // 输入视频 URL
		"-vn",                  // disable video (不要画面，只要声音)
		"-acodec", "pcm_s16le", // 强制音频编码为 16-bit PCM
		"-ar", "16000", // 强制采样率为 16000 Hz (Whisper 官方推荐标准)
		"-ac", "1", // 单声道 (双声道对语音识别没用，还浪费体积)
		"-f", "wav", // 强制打包成 wav 格式
		"-", // 🌟 核心魔法：输出到 pipe (管道)
	)

	// 将命令的标准输出和标准错误绑定到我们的内存缓冲区
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	log.Printf("开始提取音频: %s", videoURL)

	// 执行命令并等待完成
	if err := cmd.Run(); err != nil {
		// 如果失败，把 errBuf 里的 FFmpeg 原生报错日志一起打印出来，极大地降低排障难度
		return nil, fmt.Errorf("ffmpeg 提取音频失败: %v, 日志: %s", err, errBuf.String())
	}

	log.Printf("音频提取完成，大小: %d bytes", outBuf.Len())

	// 直接返回内存中的字节数组
	return outBuf.Bytes(), nil
}
