package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"go.uber.org/zap"
)

// httpClientCache 按 skipVerify 配置缓存不同 Transport 的 Client
var httpClientCache sync.Map

func getHTTPClient(skipVerify bool) *http.Client {
	key := skipVerify
	if v, ok := httpClientCache.Load(key); ok {
		return v.(*http.Client)
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	httpClientCache.Store(key, client)
	return client
}

// GitLabOAuthConfig OAuth 配置快照（从数据库实时读取）
type GitLabOAuthConfig struct {
	Enabled        bool
	BaseURL        string
	ClientID       string
	ClientSecret   string
	RedirectURI    string
	AutoCreateUser bool
	SkipVerify     bool
}

// loadOAuthConfig 从数据库加载 GitLab OAuth 配置
func loadOAuthConfig() (*GitLabOAuthConfig, error) {
	var cfg model.SystemConfig
	if err := model.DB.First(&cfg).Error; err != nil {
		return nil, fmt.Errorf("load system config failed: %w", err)
	}
	if !cfg.GitlabOAuthEnabled {
		return nil, fmt.Errorf("gitlab oauth not enabled")
	}
	return &GitLabOAuthConfig{
		Enabled:        cfg.GitlabOAuthEnabled,
		BaseURL:        cfg.GitlabBaseURL,
		ClientID:       cfg.GitlabOAuthClientID,
		ClientSecret:   cfg.GitlabOAuthClientSecret,
		RedirectURI:    cfg.GitlabOAuthRedirectURI,
		AutoCreateUser: cfg.GitlabOAuthAutoCreateUser,
		SkipVerify:     cfg.GitlabOAuthSkipVerify,
	}, nil
}

// GitLabOAuthService 处理 GitLab OAuth 流程
type GitLabOAuthService struct{}

// NewGitLabOAuthService 创建 OAuth 服务
func NewGitLabOAuthService() *GitLabOAuthService {
	return &GitLabOAuthService{}
}

// BuildAuthURL 构造 GitLab 授权 URL
func (s *GitLabOAuthService) BuildAuthURL(state string) (string, error) {
	oc, err := loadOAuthConfig()
	if err != nil {
		return "", err
	}
	baseURL := strings.TrimSuffix(oc.BaseURL, "/")

	// 基础配置校验
	if baseURL == "" {
		return "", fmt.Errorf("GitLab 地址未配置")
	}
	if oc.ClientID == "" || oc.ClientSecret == "" {
		return "", fmt.Errorf("Client ID 或 Client Secret 未配置")
	}
	// 防止把 CodeGuard 自身地址误填为 GitLab 地址
	if strings.Contains(baseURL, "/api/") || strings.HasSuffix(baseURL, "/login") {
		return "", fmt.Errorf("GitLab 地址配置错误：当前值 '%s' 看起来是 CodeGuard 自身地址，请填写 GitLab 服务器的真实地址（如 https://gitlab.company.com）", baseURL)
	}

	params := url.Values{
		"client_id":     {oc.ClientID},
		"redirect_uri":  {oc.RedirectURI},
		"response_type": {"code"},
		"state":         {state},
		"scope":         {"read_user"},
	}
	return fmt.Sprintf("%s/oauth/authorize?%s", baseURL, params.Encode()), nil
}

// ExchangeCode 用 authorization code 换 access_token
func (s *GitLabOAuthService) ExchangeCode(code string) (string, error) {
	oc, err := loadOAuthConfig()
	if err != nil {
		return "", err
	}
	baseURL := strings.TrimSuffix(oc.BaseURL, "/")
	tokenURL := fmt.Sprintf("%s/oauth/token", baseURL)

	data := url.Values{
		"client_id":     {oc.ClientID},
		"client_secret": {oc.ClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {oc.RedirectURI},
	}

	resp, err := getHTTPClient(oc.SkipVerify).Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
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
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	State     string `json:"state"`
}

// GetUserInfo 用 access_token 获取 GitLab 用户信息
func (s *GitLabOAuthService) GetUserInfo(accessToken string) (*GitLabUserInfo, error) {
	oc, err := loadOAuthConfig()
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimSuffix(oc.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/api/v4/user", baseURL)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := getHTTPClient(oc.SkipVerify)
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

	// 2. 加载配置检查是否允许自动创建
	oc, err := loadOAuthConfig()
	if err != nil {
		return nil, false, err
	}
	if !oc.AutoCreateUser {
		return nil, false, fmt.Errorf("用户不存在，请联系管理员绑定")
	}

	newUser := model.User{
		Username:       info.Username,
		DisplayName:    info.Name,
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
