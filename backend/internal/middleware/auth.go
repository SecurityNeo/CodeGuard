package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/gin-gonic/gin"
)

// GenerateToken 生成新 token（数据库持久化）
func GenerateToken(userID uint, username string) string {
	token := generateRandomToken()
	
	// 保存到数据库
	tokenModel := model.Token{
		UserID:    userID,
		Token:     token,
		Username:  username,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}
	model.DB.Create(&tokenModel)
	
	return token
}

// ValidateToken 验证 token（从数据库查询）
func ValidateToken(token string) (uint, bool) {
	if token == "" {
		return 0, false
	}
	
	var tokenModel model.Token
	// 使用当前时间（带系统时区）与数据库比较
	if err := model.DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&tokenModel).Error; err != nil {
		return 0, false
	}
	
	return tokenModel.UserID, true
}

// DeleteToken 删除 token
func DeleteToken(token string) {
	if token == "" {
		return
	}
	model.DB.Where("token = ?", token).Delete(&model.Token{})
}

// generateRandomToken 生成随机 token
func generateRandomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Auth 认证中间件
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 白名单 - 不需要认证的路径
		whiteList := []string{
			"/api/v1/login",
			"/api/v1/logout",
			"/health",
		}
		
		for _, path := range whiteList {
			if c.Request.URL.Path == path {
				c.Next()
				return
			}
		}
		
		// 静态文件也不需要认证
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}

		// 从 Header 获取 token
		token := c.GetHeader("Authorization")
		// 去除 Bearer 前缀
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		// 如果 Header 中没有有效 token，尝试从 Cookie 获取
		if token == "" {
			token, _ = c.Cookie("auth_token")
		}

		// 如果 Header/Cookie 的 token 为空或验证失败，尝试 URL query token
		// URL query token 优先级最高（调用方明确传递）
		if c.Query("token") != "" {
			queryToken := c.Query("token")
			// 如果传了 query token，直接用它覆盖（调用方明确指定）
			token = queryToken
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			c.Abort()
			return
		}

		userID, ok := ValidateToken(token)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "登录已过期，请重新登录", "token_debug": token[:8] + "...", "now": time.Now().Format(time.RFC3339)})
			c.Abort()
			return
		}

		c.Set("user_id", userID)
		c.Set("token", token)
		c.Next()
	}
}

// Logout 用户登出
func Logout(token string) {
	DeleteToken(token)
}
