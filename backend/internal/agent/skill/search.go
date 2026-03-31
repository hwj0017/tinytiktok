package skill

import (
	"fmt"

	"github.com/tmc/langchaingo/tools"
	"github.com/tmc/langchaingo/tools/duckduckgo"
)

// NewSearchTool 初始化并返回一个标准的联网搜索工具
//
// 返回值类型为 tools.Tool，这是 LangChain 中所有工具的通用接口。
// 这意味着在调用方（Agent）看来，它不在乎底层用的是 DuckDuckGo 还是 Google。
func NewSearchTool() (tools.Tool, error) {
	// 初始化 DuckDuckGo 搜索引擎
	// 参数 1: maxResults (最大返回的搜索条目数，建议 3-5 条，太多会消耗大量 Token 且容易干扰大模型)
	// 参数 2: userAgent (模拟浏览器的请求头，使用默认即可)
	maxResults := 5
	searchTool, err := duckduckgo.New(maxResults, duckduckgo.DefaultUserAgent)
	if err != nil {
		// 使用 fmt.Errorf 包装错误，方便上层排查问题出在哪里
		return nil, fmt.Errorf("初始化搜索工具失败: %w", err)
	}

	return searchTool, nil
}

/* ========================================================================
💡 生产环境替换指南 (如何换成更稳定的 Google 搜索)：

当你准备把项目部署到生产环境时，由于 DuckDuckGo 可能会有频率限制，
你可以非常轻松地把上面的代码换成 SerpAPI (Google 搜索)。

1. 引入包: "github.com/tmc/langchaingo/tools/serpapi"
2. 修改代码如下:

func NewGoogleSearchTool(apiKey string) (tools.Tool, error) {
	client, err := serpapi.New(serpapi.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}
	return client, nil
}
========================================================================
*/
