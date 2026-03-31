package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// RequestPayload 定义请求体格式
type RequestPayload struct {
	Query string `json:"query"` // 根据你的实际字段名调整
}

func main() {
	url := "http://localhost:8080/ai/chat"

	// 1. 在循环外部初始化 HTTP 客户端，明确设置 5 分钟超时
	// 放在外部可以复用底层的 TCP 连接池，提高性能
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	// 2. 初始化标准输入扫描器
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("=====================================================")
	fmt.Println("🚀 AI Agent 命令行测试工具已启动 (超时时间: 5分钟)")
	fmt.Println("💡 提示: 输入你想问的问题并回车，输入 'exit' 或 'quit' 退出")
	fmt.Println("=====================================================")

	// 3. 开启无限循环
	for {
		fmt.Print("\n👤 你的问题 👉: ")

		// 阻塞等待用户输入
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())

		// 处理空输入或退出指令
		if userInput == "" {
			continue
		}
		if strings.ToLower(userInput) == "exit" || strings.ToLower(userInput) == "quit" {
			fmt.Println("👋 退出测试，拜拜！")
			break
		}

		// 4. 构造请求数据
		payload := RequestPayload{
			Query: userInput,
		}
		jsonData, err := json.Marshal(payload)
		if err != nil {
			fmt.Printf("❌ JSON 编码失败: %v\n", err)
			continue // 不要因为一次错误退出循环，继续下一次
		}

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Printf("❌ 创建请求失败: %v\n", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		fmt.Println("⏳ Agent 正在疯狂思考和搜索中，请耐心等待...")
		startTime := time.Now()

		// 5. 发送请求
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("❌ 请求异常中断 (耗时: %v): %v\n", time.Since(startTime), err)
			// 如果是网络报错，依然 continue，让你有重试的机会
			continue
		}

		// 6. 读取并打印结果
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() // 注意：必须关闭 Body 防止内存泄漏

		if err != nil {
			fmt.Printf("❌ 读取响应失败: %v\n", err)
			continue
		}

		fmt.Printf("✅ 请求成功！(耗时: %v，HTTP状态码: %d)\n", time.Since(startTime), resp.StatusCode)
		fmt.Printf("🤖 Agent 的回答: %s\n", string(body))
		fmt.Println("-----------------------------------------------------")
	}
}
