package agent

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AIChatRequest 定义前端传过来的 JSON 请求体结构
type AIChatRequest struct {
	Query     string `json:"query" binding:"required"`
	SessionID uint   `json:"session_id"` // 可选：用于多轮对话的上下文关联
}

// AIChatHandler 控制器层，负责解析 HTTP 请求并调用底层 AI 服务
type AIChatHandler struct {
	chatService *AIChatService
}

// NewAIChatHandler 构造函数，实现依赖注入
func NewAIChatHandler(chatService *AIChatService) *AIChatHandler {
	return &AIChatHandler{
		chatService: chatService,
	}
}

// Chat 处理前端的对话请求
func (h *AIChatHandler) Chat(c *gin.Context) {
	var req AIChatRequest

	// 1. 绑定并校验前端传来的 JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "请求参数错误: " + err.Error(),
		})
		return
	}

	// 2. 将带有超时的 HTTP Context 透传给底层的 AI 服务
	answer, err := h.chatService.ChatWithAgent(c.Request.Context(), req.Query, req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": "AI 处理失败: " + err.Error(),
		})
		return
	}

	// 3. 组装成功的数据结构返回给前端
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data": gin.H{
			"answer": answer,
		},
	})
}
