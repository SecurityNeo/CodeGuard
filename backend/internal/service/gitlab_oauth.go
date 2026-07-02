package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ai-optimizer/backend/config"
	"github.com/ai-optimizer/backend/internal/model"
	"go.uber.org/zap"
)

// 包级别带超时的 HTTP Client
var oauthHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GitLabOAuthService 处理 GitLab OAuth 流程
type GitLabOAuthService struct {
	cfg *config.Config
}

// NewGitLabOAuthService 创建 OAuth 服务（依赖注入）
func NewGitLabOAuthService(cfg *config.Config) *GitLabOAuthService {
	return &GitLabOAuthService{cfg: cfg}
}

// BuildAuthURL 构造 GitLab 授权 URL
func (s *GitLabOAuthService) BuildAuthURL(state string) string {
	baseURL := strings.TrimSuffix(s.cfg.GitlabBaseURL, "/")
	params := url.Values{
		"client_id":     {s.cfg.GitlabOAuthClientID},
		"redirect_uri":  {s.cfg.GitlabOAuthRedirectURI},
		"response_type": {"code"},
		"state":         {state},
		"scope":         {"read_user"},
	}
	return fmt.Sprintf("%s/oauth/authorize?%s", baseURL, params.Encode())
}

// ExchangeCode 用 authorization code 换 access_token
func (s *GitLabOAuthService) ExchangeCode(code string) (string, error) {
	baseURL := strings.TrimSuffix(s.cfg.GitlabBaseURL, "/")
	tokenURL := fmt.Sprintf("%s/oauth/token", baseURL)

	data := url.Values{
		"client_id":     {s.cfg.GitlabOAuthClientID},
		"client_secret": {s.cfg.GitlabOAuthClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {s.cfg.GitlabOAuthRedirectURI},
	}

	resp, err := oauthHTTPClient.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gitlab token endpoint returned %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token response failed: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access_token")
	}

	return tokenResp.AccessToken, nil
}

// GitLabUserInfo GitLab API /api/v4/user 返回的结构
type GitLabUserInfo struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	State    string `json:"state"`
}

// GetUserInfo 用 access_token 获取 GitLab 用户信息
func (s *GitLabOAuthService) GetUserInfo(accessToken string) (*GitLabUserInfo, error) {
	baseURL := strings.TrimSuffix(s.cfg.GitlabBaseURL, "/")
	apiURL := fmt.Sprintf("%s/api/v4/user", baseURL)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := oauthHTTPClient
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get user info failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitlab user endpoint returned %d", resp.StatusCode)
	}

	var userInfo GitLabUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("decode user info failed: %w", err)
	}

	if userInfo.ID == 0 {
		return nil, fmt.Errorf("invalid user info: empty id")
	}

	return &userInfo, nil
}

// FindOrCreateUser 根据 GitLab 用户信息查找或创建本地用户
func (s *GitLabOAuthService) FindOrCreateUser(info *GitLabUserInfo) (*model.User, bool, error) {
	// 1. 按 GitlabUserID 精确匹配
	var user model.User
	if err := model.DB.Where("gitlab_user_id = ?", info.ID).First(&user).Error; err == nil {
		return &user, false, nil // 已有用户
	}

	// 2. 未找到，自动创建新用户
	if !s.cfg.GitlabOAuthAutoCreateUser {
		return nil, false, fmt.Errorf("用户不存在，请联系管理员绑定")
	}

	newUser := model.User{
		Username:       info.Username,
		Role:           model.RoleUser,
		LoginType:      "gitlab",
		GitlabUserID:   func() *uint64 { v := uint64(info.ID); return &v }(),
		GitlabUsername: info.Username,
		GitlabEmail:    info.Email,
		AvatarURL:      info.AvatarURL,
	}

	// 用户名冲突处理：如果已有同名本地用户，追加 _gitlab
	var count int64
	model.DB.Model(&model.User{}).Where("username = ?", info.Username).Count(&count)
	if count > 0 {
		newUser.Username = info.Username + "_gitlab"
	}

	if err := model.DB.Create(&newUser).Error; err != nil {
		zap.L().Error("create gitlab user failed", zap.Error(err))
		return nil, false, fmt.Errorf("创建用户失败: %w", err)
	}

	return &newUser, true, nil
}
