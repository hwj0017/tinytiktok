package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"feedsystem_video_go/internal/middleware/asr"
	"feedsystem_video_go/internal/middleware/embedding"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	"feedsystem_video_go/internal/middleware/vectordb"
	"feedsystem_video_go/internal/video"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

type RagWorker struct {
	ch        *amqp.Channel
	asr       *asr.ASRProvider
	embedding *embedding.EmbeddingProvider
	vectordb  *vectordb.VectorDBProvider
	videos    *video.VideoRepository
	queue     string
}

var ragNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

func NewRagWorker(ch *amqp.Channel, asr *asr.ASRProvider, embedding *embedding.EmbeddingProvider, vectordb *vectordb.VectorDBProvider, videos *video.VideoRepository, queue string) *RagWorker {
	return &RagWorker{ch: ch, asr: asr, embedding: embedding, vectordb: vectordb, videos: videos, queue: queue}
}

func (w *RagWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.vectordb == nil || w.videos == nil {
		return errors.New("Rag worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	deliveries, err := w.ch.Consume(
		w.queue,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("deliveries channel closed")
			}
			w.handleDelivery(ctx, d)
		}
	}
}

func (w *RagWorker) handleDelivery(ctx context.Context, d amqp.Delivery) {
	var evt rabbitmq.RagTaskEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		// 解析事件失败，直接丢弃，不重试
		return
	}
	if evt.VideoID == 0 {
		return
	}
	if evt.Action == rabbitmq.ActionPublish {
		if err := w.publish(ctx, evt.VideoID); err != nil {
			return
		}
	}
	if evt.Action == rabbitmq.ActionDelete {
		if err := w.delete(ctx, evt.VideoID); err != nil {
			return
		}
	}
	_ = d.Ack(false)
}

// publish 处理 RAG 核心任务链：查询 -> 提音 -> 识别 -> 向量化 -> 存储
func (w *RagWorker) publish(ctx context.Context, video_id uint) error {

	log.Printf("Rag worker: start publishing video_id=%d", video_id)

	// 1. 根据 ID 查视频信息
	// 假设 videos repository 有类似 GetByID 的方法，并且返回包含文件路径(FilePath)或URL的结构
	vid, err := w.videos.GetByID(ctx, video_id)
	if err != nil {
		return fmt.Errorf("get video info failed: %w", err)
	}

	// 2. 提取音频
	// 调用下方的 FFmpeg 辅助函数，直接把音频提取到内存中
	audioData, err := ExtractAudio(ctx, vid.PlayURL)
	if err != nil {
		return fmt.Errorf("extract audio failed: %w", err)
	}
	// 3. 语音识别 (ASR)
	filename := fmt.Sprintf("video_%d.wav", video_id)
	whisperData, err := w.asr.RecognizeSpeech(ctx, audioData, filename)
	if err != nil {
		return fmt.Errorf("asr publishing failed: %w", err)
	}
	if whisperData == nil || len(whisperData.Segments) == 0 {
		return nil // 没有识别出内容
	}

	log.Printf("Rag worker: video_id=%d splitted into %d semantic chunks by Whisper", video_id, len(whisperData.Segments))
	// ==========================================
	// 核心改造：使用滑动窗口合并相邻的 Segments
	// ==========================================

	windowSize := 6 // 每次合并 6 句话 (通常 Whisper 6句话大概是 20~30 秒，非常适合做上下文)
	stepSize := 4   // 每次向后滑动 4 句话 (意味着相邻的 Chunk 会有 2 句话的“重叠区”)

	// chunkIndex 用于生成 UUID 和 Payload
	chunkIndex := 0

	for i := 0; i < len(whisperData.Segments); i += stepSize {
		// 计算当前窗口的结束位置
		end := i + windowSize
		if end > len(whisperData.Segments) {
			end = len(whisperData.Segments)
		}

		// 1. 提取当前窗口的起始时间和结束时间
		startTime := whisperData.Segments[i].Start
		endTime := whisperData.Segments[end-1].End

		// 2. 将这几句话的文本拼接起来
		var sb strings.Builder
		for j := i; j < end; j++ {
			text := strings.TrimSpace(whisperData.Segments[j].Text)
			if text != "" {
				sb.WriteString(text + " ")
			}
		}

		mergedText := strings.TrimSpace(sb.String())

		// 忽略极短的无意义段落 (比如连拼了6句全是空白或语气词)
		if len(mergedText) < 10 {
			if end == len(whisperData.Segments) {
				break
			}
			continue
		}

		// 3. 向量化合并后的长文本 (现在语义非常丰富了！)
		vector, err := w.embedding.EmbedText(ctx, mergedText)
		if err != nil {
			log.Printf("embed chunk %d failed: %v", chunkIndex, err)
			continue
		}

		// 4. 生成绝对唯一的 UUID (视频ID + 窗口切片序号)
		uniqueStr := fmt.Sprintf("vid_%d_chunk_%d", video_id, chunkIndex)
		pointID := uuid.NewSHA1(ragNamespace, []byte(uniqueStr)).String()

		// 5. 构造 Payload (时间和文本都变成了合并后的)
		payload := map[string]any{
			"video_id":    video_id,
			"chunk_index": chunkIndex,
			"start_time":  uint64(startTime * 1000), // 毫秒级
			"end_time":    uint64(endTime * 1000),   // 毫秒级
			"text":        mergedText,               // 拼接后的完整段落
		}

		// 6. 存入向量数据库
		err = w.vectordb.Upsert(ctx, pointID, vector, payload)
		if err != nil {
			log.Printf("upsert chunk %d to db failed: %v", chunkIndex, err)
			continue
		}

		chunkIndex++

		// 如果已经滑动到了最后，退出循环
		if end == len(whisperData.Segments) {
			break
		}
	}

	log.Printf("Rag worker: successfully published video_id=%d", video_id)
	return nil
}

// extractAudio 辅助函数：使用 FFmpeg 将视频中的音频提取为 16kHz, 单声道, 16bit 的 WAV 格式，直接返回 byte 切片
func ExtractAudio(ctx context.Context, videoPath string) ([]byte, error) {
	// 使用命令: ffmpeg -i input.mp4 -vn -acodec pcm_s16le -ar 16000 -ac 1 -f wav pipe:1
	// -vn: 忽略视频流
	// -acodec pcm_s16le: ASR 模型通常最喜欢这种 16-bit PCM 格式
	// -ar 16000 -ac 1: 16kHz 采样率，单声道 (Whisper 的标准输入)
	// -f wav pipe:1: 输出为 wav 格式并直接写入标准输出流，避免落盘

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-f", "wav",
		"pipe:1",
	)

	var audioBuffer bytes.Buffer
	var errBuffer bytes.Buffer
	cmd.Stdout = &audioBuffer // 音频数据写进 buffer
	cmd.Stderr = &errBuffer   // FFmpeg 的日志写到 errBuffer (排错用)

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg run failed: %v, stderr: %s", err, errBuffer.String())
	}

	return audioBuffer.Bytes(), nil
}

func (w *RagWorker) delete(ctx context.Context, video_id uint) error {

	log.Printf("🗑️ [RAG Worker 启动] 开始清理视频 ID=%s 的所有向量切片数据...", video_id)

	// 2. 调用 Qdrant 数据库提供者执行物理删除
	// 这里直接复用我们之前写好的按 payload 过滤删除的方法
	err := w.vectordb.DeleteVideoChunks(ctx, video_id)
	if err != nil {
		// 记录详细日志，方便后续排查
		log.Printf("❌ [RAG Worker 失败] 删除视频 ID=%s 向量数据异常: %v\n", video_id, err)

		// ⚠️ 极其重要的一步：将错误 return 出去！
		// 如果你使用 RabbitMQ，返回 error 会让消息队列认为消费失败，从而触发重试机制（Nack/Requeue）。
		// 这样能保证就算网络抖动，数据最终也一定会被删掉。
		return fmt.Errorf("qdrant 级联删除失败: %w", err)
	}

	// 3. 删除成功，记录日志
	// 此时 RabbitMQ 收到 nil 错误，会自动 ACK，这条消息就算彻底消费完成了
	log.Printf("✅ [RAG Worker 完毕] 视频 ID=%s 的幽灵切片数据已彻底抹除！\n", video_id)
	return nil
}
