package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	es "feedsystem_video_go/internal/middleware/elasticsearch"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/tmc/langchaingo/tools"
	"github.com/tmc/langchaingo/vectorstores"
)

// 确保实现接口
var _ tools.Tool = (*SearchVideoTool)(nil)

type SearchVideoTool struct {
	// 🌟 核心变化：直接引用标准接口
	vectorStore vectorstores.VectorStore
	esClient    *elasticsearch.Client
}

func NewSearchVideoTool(v vectorstores.VectorStore, es *elasticsearch.Client) *SearchVideoTool {
	return &SearchVideoTool{
		vectorStore: v,
		esClient:    es,
	}
}

func (t *SearchVideoTool) Name() string {
	return "search_video_knowledge"
}

func (t *SearchVideoTool) Description() string {
	return `当你需要搜索视频库中的知识、画面描述或语音对话时调用。
我会通过语义理解和关键词匹配，返回最相关的视频片段及时间点。`
}

func (t *SearchVideoTool) Call(ctx context.Context, input string) (string, error) {
	query := strings.Trim(input, `"'`)
	log.Printf("🛠️ [Hybrid Search] 正在执行双路召回: %s\n", query)

	var sb strings.Builder
	sb.WriteString("### 检索到的参考事实如下：\n")

	// --- 1. 第一路：VectorStore 语义召回 ---
	// 🌟 优势：无需手动 EmbedText，接口内部自动处理
	docs, err := t.vectorStore.SimilaritySearch(ctx, query, 3)
	if err == nil && len(docs) > 0 {
		sb.WriteString("\n**【语义相关片段】**\n")
		for _, doc := range docs {
			// 从 Metadata 中提取我们之前存入的强类型数据
			m := doc.Metadata
			startTime := m["start_time"].(uint64)
			videoID := m["video_id"].(uint)

			sb.WriteString(fmt.Sprintf("- [视频ID:%d, 开始时间:%.1fs]: %s\n",
				videoID, float64(startTime)/1000.0, doc.PageContent))
		}
	}

	// --- 2. 第二路：Elasticsearch 关键词召回 ---
	esHits, err := t.searchFromES(ctx, query)
	if err == nil && len(esHits) > 0 {
		sb.WriteString("\n**【精准匹配视频】**\n")
		for _, h := range esHits {
			sb.WriteString(fmt.Sprintf("- [视频ID:%d] 标题: %s\n  摘要: %s\n",
				h.VideoID, h.Title, h.Summary))
		}
	}

	// --- 3. 结果合并与兜底 ---
	if sb.Len() < 50 {
		return "未找到相关视频内容，请告知用户库中暂无此信息。", nil
	}

	return sb.String(), nil
}

// searchFromES 保持不变，执行 ES 的多路模糊匹配
func (t *SearchVideoTool) searchFromES(ctx context.Context, query string) ([]esResult, error) {
	queryBody := map[string]any{
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":     query,
				"fields":    []string{"title^3", "description^2", "asr_text", "ocr_text"},
				"fuzziness": "AUTO",
			},
		},
		"size": 3,
	}

	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(queryBody)

	res, err := t.esClient.Search(
		t.esClient.Search.WithContext(ctx),
		t.esClient.Search.WithIndex(es.EsIndex),
		t.esClient.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	// 3. 检查响应状态
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("es error response [%d]: %s", res.StatusCode, string(body))
	}

	// 4. 解析结果集
	var r struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("failed to decode es response: %w", err)
	}

	var results []esResult
	for _, hit := range r.Hits.Hits {
		var item es.FullVideoEvent
		if err := json.Unmarshal(hit.Source, &item); err != nil {
			log.Printf("⚠️ 序列化 ES 单条记录失败: %v", err)
			continue
		}

		// 5. 组装结果并生成摘要 (Summary)
		// 优先取 ASR 文本，如果没有则取 OCR 文本
		summary := item.ASRText
		if summary == "" {
			summary = item.OCRText
		}

		results = append(results, esResult{
			VideoID: item.VideoID,
			Title:   item.Title,
			Summary: truncate(summary, 150), // 限制给大模型的上下文长度，防止 Token 爆炸
		})
	}
	return results, nil
}

type esResult struct {
	VideoID uint
	Title   string
	Summary string
}

// truncate 辅助函数：截断字符串并添加省略号
func truncate(s string, n int) string {
	runeS := []rune(s)
	if len(runeS) <= n {
		return s
	}
	return string(runeS[:n]) + "..."
}
