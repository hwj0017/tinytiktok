package agent

import "github.com/tmc/langchaingo/llms"

// VideoSegment 存储结构
type VideoSegment struct {
	VideoID string `json:"video_id"`
	// StartTime 存储单位：毫秒 (ms)
	StartTime uint64 `json:"start_time"`
	// EndTime 存储单位：毫秒 (ms)
	EndTime uint64 `json:"end_time"`
	Content string `json:"content"`
}

// SearchToolDefinition
var SearchToolDefinition = llms.Tool{
	Type: "function",
	Function: &llms.FunctionDefinition{
		Name:        "search_video_knowledge",
		Description: "检索视频内的具体知识片段，如食材切法、烹饪时长等。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词",
				},
			},
			"required": []string{"query"},
		},
	},
}
