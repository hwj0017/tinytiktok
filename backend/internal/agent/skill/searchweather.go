package skill

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tmc/langchaingo/tools"
)

// 确保 SearchWeatherTool 实现了 langchaingo 的 tools.Tool 接口
var _ tools.Tool = (*SearchWeatherTool)(nil)

// SearchWeatherTool 实现了查询天气的标准技能工具
type SearchWeatherTool struct {
	// 如果你后续需要接入和风天气 API、OpenWeather，或者使用 Redis 缓存，
	// 可以将对应的 Client 或 Provider 作为依赖放在这里。
	// 例如：redisClient *redis.Client
}

// NewSearchWeatherTool 初始化天气技能
func NewSearchWeatherTool() *SearchWeatherTool {
	return &SearchWeatherTool{}
}

// Name 必须返回工具的英文名，供大模型在 JSON 中指定调用
func (t *SearchWeatherTool) Name() string {
	return "search_weather"
}

// Description 极其重要！这是给大模型看的说明书，决定了它在什么情况下会触发这个技能
func (t *SearchWeatherTool) Description() string {
	return `当用户询问某个城市、地区的天气情况（如温度、下雨、天气预报等）时，必须调用此工具。
请不要根据你的训练数据编造过时的天气。
输入参数应当是你从用户问题中提炼出的【纯城市名称】（例如用户问“北京今天天气怎么样”，输入应为“北京”；问“纽约冷吗”，输入应为“纽约”）。`
}

// Call 核心逻辑：大模型决定使用本工具后，LangChainGo 底层会自动执行这个函数
func (t *SearchWeatherTool) Call(ctx context.Context, input string) (string, error) {
	// input 是大模型提取并传递过来的城市名
	// 清理大模型可能传过来的带引号或多余空格的字符串
	city := strings.Trim(strings.TrimSpace(input), `"'`)
	if city == "" {
		return "未提取到有效的城市名称，请询问用户具体想查哪个城市的天气。", nil
	}

	fmt.Printf("🌤️ [Skill 触发] 大模型正在查询天气，目标城市: %s\n", city)

	// 1. 调用外部天气 API
	// 这里使用开源免费的 wttr.in 作为演示，它会直接返回文本格式的天气。
	// 格式化参数：当前位置/城市, 天气icon, 温度, 风速, 湿度
	// 注意：生产环境中，建议替换为【和风天气】等商业 API，并在这里加入 Redis 缓存逻辑避免被限流。
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=%%l:++%%c+%%t++风速:%%w++湿度:%%h", url.PathEscape(city))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("构建天气请求失败: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求天气接口失败: %w", err)
	}
	defer resp.Body.Close()

	// 处理 HTTP 错误或查不到该城市的情况
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("无法获取【%s】的天气数据，可能是城市名不合法或服务暂时不可用，请如实告知用户。", city), nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取天气数据失败: %w", err)
	}

	weatherInfo := string(bodyBytes)

	// 2. 格式化检索结果给大模型
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("以下是获取到的【%s】实时天气信息：\n", city))
	sb.WriteString(weatherInfo)

	// 把拼接好的事实数据返回给大模型
	return sb.String(), nil
}
