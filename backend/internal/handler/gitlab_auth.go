package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ai-optimizer/backend/internal/middleware"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type GitLabAuthHandler struct {
	oauthSvc *service.GitLabOAuthService
}

// stateCache 内存缓存 state（无 Redis 时使用），带读写锁
type stateCache struct {
	mu     sync.RWMutex
	states map[string]stateEntry
}
type stateEntry struct {
	ip     string
	expire time.Time
}

var cache = &stateCache{
	states: make(map[string]stateEntry),
}

func init() {
	// 启动定时清理，每 5 分钟清理过期 state
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cache.cleanup()
		}
	}()
}

func (c *stateCache) add(state, ip string, expire time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states[state] = stateEntry{ip: ip, expire: expire}
}

func (c *stateCache) get(state string) (stateEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.states[state]
	return entry, ok
}

func (c *stateCache) del(state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.states, state)
}

func (c *stateCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.states {
		if now.After(v.expire) {
			delete(c.states, k)
		}
	}
}

func NewGitLabAuthHandler() *GitLabAuthHandler {
	return &GitLabAuthHandler{
		oauthSvc: service.NewGitLabOAuthService(),
	}
}

func generateState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极端 fallback：用时间戳 + 随机数
		return fmt.Sprintf("%d%x", time.Now().UnixNano(), b)
	}
	return hex.EncodeToString(b)
}

// Redirect GET /api/v1/auth/gitlab
func (h *GitLabAuthHandler) Redirect(c *gin.Context) {
	state := generateState()
	cache.add(state, "", time.Now().Add(5*time.Minute))
	authURL, err := h.oauthSvc.BuildAuthURL(state)
	if err != nil {
		c.JSON(400, gin.H{"error": "GitLab OAuth 未启用或配置不完整: " + err.Error()})
		return
	}
	c.Redirect(http.StatusFound, authURL)
}

// Callback GET /api/v1/auth/gitlab/callback
func (h *GitLabAuthHandler) Callback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")

	if code == "" || state == "" {
		c.JSON(400, gin.H{"error": "缺少 code 或 state"})
		return
	}

	_, ok := cache.get(state)
	if !ok {
		c.JSON(403, gin.H{"error": "state 无效或已过期"})
		return
	}
	cache.del(state)

	accessToken, err := h.oauthSvc.ExchangeCode(code)
	if err != nil {
		zap.L().Error("gitlab oauth exchange code failed", zap.Error(err))
		c.JSON(500, gin.H{"error": "GitLab 认证失败: " + err.Error()})
		return
	}

	userInfo, err := h.oauthSvc.GetUserInfo(accessToken)
	if err != nil {
		zap.L().Error("gitlab get user info failed", zap.Error(err))
		c.JSON(500, gin.H{"error": "获取用户信息失败: " + err.Error()})
		return
	}

	user, _, err := h.oauthSvc.FindOrCreateUser(userInfo)
	if err != nil {
		// 区分"未启用"和"用户不存在"两种错误
		errMsg := err.Error()
		status := 403
		if errMsg == "gitlab oauth not enabled" {
			errMsg = "GitLab OAuth 未启用"
		}
		c.JSON(status, gin.H{"error": errMsg})
		return
	}

	token := middleware.GenerateToken(user.ID, user.Username)

	zap.L().Info("gitlab login success",
		zap.String("username", user.Username),
		zap.String("gitlab_username", user.GitlabUsername),
	)

	// 设置 Cookie + 重定向到首页，URL 附带 token 供前端 localStorage 使用
	c.SetCookie("auth_token", token, 7*24*3600, "/", "", false, false)
	c.Redirect(http.StatusFound, "/index.html?token="+token)
}
