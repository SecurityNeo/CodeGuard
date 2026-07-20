package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/llm"
	"github.com/ai-optimizer/backend/pkg/llmcall"
	"go.uber.org/zap"
)

// CallStatus 调用状态常量
const (
	CallStatusSuccess = "success"
	CallStatusFailed  = "failed"
	CallStatusUnknown = "unknown"
)

type LLMService struct{}

func NewLLMService() *LLMService {
	return &LLMService{}
}

// sharedHTTPClient 复用的 HTTP 客户端（带连接池），按调用覆盖 timeout。
// 每个调用单独覆盖 Timeout，避免共享 client 的 timeout 被覆盖后无法恢复。
var sharedHTTPTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 20,
	IdleConnTimeout:     90 * time.Second,
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: sharedHTTPTransport,
	}
}

type ChatResult struct {
	Content   string
	ModelID   uint   // 实际使用的模型 ID
	ModelName string // 实际使用的模型标识名
}

// StructuredChatResult 结构化输出结果
type StructuredChatResult struct {
	Content   string
	ModelID   uint
	ModelName string
	Response  *llm.ChatResponse // 完整响应（含 Refusal）
}

// ModelAttempt 描述一次 LLM 模型调用尝试。
type ModelAttempt struct {
	Role    string // "主模型" / "备用[1]" / ...
	ModelID uint
	Model   string // LLMModel.ModelID（provider/model 标识）
	Err     error
}

// ChainError 多模型链式调用全部失败时聚合的错误。
// 替代"主模型和所有备用模型均不可用"这类丢信息的通用错误，
// 让任务 ErrorMsg 携带每个尝试的角色/模型/具体错误，便于前端展示和运维排查。
type ChainError struct {
	Attempts []ModelAttempt
}

// Error 输出多行文本，每行一个模型尝试。
func (e *ChainError) Error() string {
	var b strings.Builder
	for _, a := range e.Attempts {
		fmt.Fprintf(&b, "%s %s (#%d): %v\n", a.Role, a.Model, a.ModelID, a.Err)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Unwrap 返回第一个底层 error，保留 errors.Is/As 兼容性。
func (e *ChainError) Unwrap() error {
	if len(e.Attempts) == 0 {
		return nil
	}
	return e.Attempts[0].Err
}

// sysCfgCache 缓存 SystemConfig，避免每次 LLM 调用都查询数据库。
// 使用 atomic.Value 提供 lock-free 读；写由后台 goroutine + TTL 触发。
type sysCfgCacheEntry struct {
	taskTimeoutMin int
	fetchedAt      time.Time
}

var (
	sysCfgCache     atomic.Pointer[sysCfgCacheEntry]
	// sysCfgCacheTTL 必须大于 cron 刷新周期，否则 cache 长时间处于"已过期"状态，
	// loadSysCfgCached 返回 nil，callLLMAPI 会回退到 llmModel.TimeoutSec 默认值（120s），
	// 导致系统配置 task_timeout_min 不生效。
	sysCfgCacheTTL  = 5 * time.Minute
	sysCfgCacheOnce atomic.Bool
)

// ChatCompletion 直接调用大模型 Chat Completion API
// taskID 为关联任务 ID（用于 Token 用量统计），传 nil 表示无关联任务
// caller 用于在 Token 用量日志中标记调用方（runAIReview / runAIReviewStructured / runAIReviewFallback / retry）
// modelID: 指定模型 ID
//   - modelID > 0: 强制使用指定模型，失败直接报错（不走主备）
//   - modelID == 0: 走全局主备链路（主模型 → 备用1 → 备用2...）
func (s *LLMService) ChatCompletion(taskID *uint, modelID uint, caller, systemPrompt, userPrompt string) (*ChatResult, error) {
	// ① 用户强制指定了模型 → 直接调用，不走主备
	if modelID > 0 {
		resp, m, err := s.callSpecificModel(taskID, modelID, caller, systemPrompt, userPrompt, nil)
		if err != nil {
			return nil, err
		}
		return &ChatResult{Content: resp.Choices[0].Message.Content, ModelID: m.ID, ModelName: m.ModelID}, nil
	}

	// ② 未指定 modelID → 走全局主备链路
	resp, m, err := s.tryChain(taskID, nil, caller, systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	return &ChatResult{Content: resp.Choices[0].Message.Content, ModelID: m.ID, ModelName: m.ModelID}, nil
}

// callSpecificModel 调用指定 ID 的模型，失败时返回"指定模型 X (#Y): <err>"格式错误。
func (s *LLMService) callSpecificModel(taskID *uint, modelID uint, caller, systemPrompt, userPrompt string, responseFormat *llm.ResponseFormat) (*llm.ChatResponse, *model.LLMModel, error) {
	var m model.LLMModel
	if err := model.DB.First(&m, modelID).Error; err != nil {
		return nil, nil, fmt.Errorf("指定的模型不存在: %w", err)
	}
	if m.Status != "active" {
		return nil, nil, fmt.Errorf("指定的模型[%s]当前状态异常: %s", m.ModelID, m.Status)
	}
	content, err := s.callLLMAPI(taskID, &m, caller, systemPrompt, userPrompt, responseFormat)
	if err != nil {
		return nil, &m, fmt.Errorf("指定模型 %s (#%d): %w", m.ModelID, m.ID, err)
	}
	if len(content.Choices) == 0 {
		return nil, &m, errors.New("LLM returned empty choices")
	}
	return content, &m, nil
}

// tryChain 主备链调用尝试：依次尝试主模型和所有备用模型，记录每个尝试的错误。
// 成功时返回 resp 和所用模型；全部失败时返回 *ChainError。
func (s *LLMService) tryChain(taskID *uint, responseFormat *llm.ResponseFormat, caller, systemPrompt, userPrompt string) (*llm.ChatResponse, *model.LLMModel, error) {
	attempts := make([]ModelAttempt, 0, 4)

	// ① 主模型
	var primary model.LLMModel
	if err := model.DB.Where("is_primary = ? AND status = ?", true, "active").First(&primary).Error; err == nil {
		resp, callErr := s.callLLMAPI(taskID, &primary, caller, systemPrompt, userPrompt, responseFormat)
		if callErr == nil && len(resp.Choices) > 0 {
			zap.L().Info("主模型调用成功",
				zap.Uint("model_id", primary.ID),
				zap.String("model", primary.ModelID))
			return resp, &primary, nil
		}
		// 失败或空 choices：归一为 attempt 错误
		if callErr == nil {
			callErr = errors.New("LLM returned empty choices")
		}
		attempts = append(attempts, ModelAttempt{
			Role: "主模型", ModelID: primary.ID, Model: primary.ModelID, Err: callErr,
		})
		zap.L().Warn("主模型调用失败，准备切换备用",
			zap.Uint("model_id", primary.ID),
			zap.String("model", primary.ModelID),
			zap.Error(callErr))
	} else {
		zap.L().Warn("未找到可用的主模型，直接尝试备用模型", zap.Error(err))
	}

	// ② 按 backup_order 遍历备用
	var backups []model.LLMModel
	model.DB.Where("backup_order > 0 AND status = ?", "active").Order("backup_order ASC, id ASC").Find(&backups)

	for i, b := range backups {
		resp, callErr := s.callLLMAPI(taskID, &b, caller, systemPrompt, userPrompt, responseFormat)
		if callErr == nil && len(resp.Choices) > 0 {
			zap.L().Info("备用模型调用成功",
				zap.Int("backup_index", i+1),
				zap.Uint("model_id", b.ID),
				zap.String("model", b.ModelID))
			return resp, &b, nil
		}
		if callErr == nil {
			callErr = errors.New("LLM returned empty choices")
		}
		attempts = append(attempts, ModelAttempt{
			Role: fmt.Sprintf("备用[%d]", i+1), ModelID: b.ID, Model: b.ModelID, Err: callErr,
		})
		zap.L().Warn("备用模型调用失败，继续下一个",
			zap.Int("backup_index", i+1),
			zap.Uint("model_id", b.ID),
			zap.String("model", b.ModelID),
			zap.Error(callErr))
	}

	if len(attempts) == 0 {
		return nil, nil, errors.New("未配置任何活跃的大模型（无主模型也无可用备用）")
	}
	return nil, nil, &ChainError{Attempts: attempts}
}

// callLLMAPI 实际发起 HTTP 调用（内部辅助函数）
// responseFormat 为 nil 时使用普通文本输出
// taskID 用于关联 Token 用量日志，nil 表示无任务关联
// caller 用于在 Token 用量日志中标记调用方（为空时记 "unknown"）
func (s *LLMService) callLLMAPI(taskID *uint, llmModel *model.LLMModel, caller, systemPrompt, userPrompt string, responseFormat *llm.ResponseFormat) (respResult *llm.ChatResponse, retErr error) {
	if llmModel == nil {
		return nil, errors.New("callLLMAPI: llmModel is nil")
	}
	if strings.TrimSpace(llmModel.APIKey) == "" {
		return nil, fmt.Errorf("model[%s] api_key is empty", llmModel.ModelID)
	}
	start := time.Now()
	if caller == "" {
		caller = CallStatusUnknown
	}
	zap.L().Info("calling LLM API",
		zap.Uint("model_id", llmModel.ID),
		zap.String("provider", llmModel.Provider),
		zap.String("model", llmModel.ModelID))

	// defer 记录 Token 用量，覆盖成功/失败两条分支
	// invariant: 失败路径 return nil, err → respResult 必为 nil；
	//            成功路径 return &result, nil → retErr 必为 nil；二者互斥。
	defer func() {
		duration := time.Since(start)
		record := llmcall.RecordRequest{
			TaskID:     taskID,
			ModelID:    &llmModel.ID,
			Provider:   llmModel.Provider,
			ModelName:  llmModel.ModelID,
			CallType:   model.CallTypeScore,
			Caller:     caller,
			DurationMs: int(duration.Milliseconds()),
		}
		if retErr != nil {
			record.Status = CallStatusFailed
			record.ErrorMsg = sanitizeForLog(retErr.Error())
		} else if respResult != nil {
			record.Status = CallStatusSuccess
			record.PromptTokens = respResult.Usage.PromptTokens
			record.CompletionTokens = respResult.Usage.CompletionTokens
			record.CachedTokens = respResult.Usage.CachedTokens
			// 优先用 LLM 返回的 total_tokens（含 reasoning 等），缺失时回退到求和
			if respResult.Usage.TotalTokens > 0 {
				record.TotalTokens = respResult.Usage.TotalTokens
			} else {
				record.TotalTokens = record.PromptTokens + record.CompletionTokens
			}
			record.CostCents = calcCostCents(llmModel, record.PromptTokens, record.CompletionTokens, record.CachedTokens)
		} else {
			record.Status = CallStatusUnknown
		}
		llmcall.Record(record)
	}()

	apiKey := llmModel.APIKey
	baseURL := strings.TrimRight(llmModel.BaseURL, "/")
	chatPath := "/v1/chat/completions"
	if strings.HasSuffix(baseURL, "/v1") {
		chatPath = "/chat/completions"
	}
	url := baseURL + chatPath

	reqBody := map[string]interface{}{
		"model": llmModel.ModelID,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": llmModel.Temperature,
		"max_tokens":  llmModel.MaxTokens,
	}

	// 传入结构化输出格式（如 json_schema）
	if responseFormat != nil {
		reqBody["response_format"] = responseFormat
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	// AI 评审任务超时时间使用系统配置中的 task_timeout_min
	timeoutSec := llmModel.TimeoutSec
	if cached := loadSysCfgCached(); cached != nil && cached.taskTimeoutMin > 0 {
		timeoutSec = cached.taskTimeoutMin * 60
	} else {
		// cache 未命中或 task_timeout_min=0：显式 warn，便于排查"配置不生效"问题
		zap.L().Warn("sysCfgCache miss, using model default timeout",
			zap.Uint("model_id", llmModel.ID),
			zap.Int("llm_model_timeout_sec", llmModel.TimeoutSec))
	}

	client := newHTTPClient(time.Duration(timeoutSec) * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read LLM response body failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API error: status=%d, body=%s", resp.StatusCode, sanitizeForLog(string(respBody)))
	}

	var result llm.ChatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse LLM response failed: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, errors.New("LLM returned empty choices")
	}

	return &result, nil
}

// loadSysCfgCached 读取缓存的 SystemConfig，过期或未初始化时返回 nil（调用方按 nil 处理）。
func loadSysCfgCached() *sysCfgCacheEntry {
	entry := sysCfgCache.Load()
	if entry == nil {
		return nil
	}
	if time.Since(entry.fetchedAt) > sysCfgCacheTTL {
		return nil
	}
	return entry
}

// RefreshSysCfgCache 主动刷新缓存（main.go 启动后调用一次 + 可选定时刷新）。
func RefreshSysCfgCache() {
	var sysCfg model.SystemConfig
	if err := model.DB.First(&sysCfg).Error; err != nil {
		zap.L().Warn("refresh sys cfg cache failed", zap.Error(err))
		return
	}
	sysCfgCache.Store(&sysCfgCacheEntry{
		taskTimeoutMin: sysCfg.TaskTimeoutMin,
		fetchedAt:      time.Now(),
	})
	if sysCfgCacheOnce.CompareAndSwap(false, true) {
		zap.L().Info("sys cfg cache initialized",
			zap.Int("task_timeout_min", sysCfg.TaskTimeoutMin))
	}
}

// calcCostCents 根据模型价格计算成本（单位：分）。
// 价格 0 表示未配置，不计入成本；返回 0 而非错误。
// 公式：input_cost = input_tokens / 1e6 * input_price * 100 (USD→cents)
//       cached_cost = cached_tokens / 1e6 * cached_price * 100
//       output_cost = completion_tokens / 1e6 * output_price * 100
//       total_cents = input_cost + output_cost - input_cost（cached 部分）+ cached_cost
//       即：把缓存命中部分按 cached_price 单独计费，避免重复计算 input。
func calcCostCents(m *model.LLMModel, promptTokens, completionTokens, cachedTokens int) int64 {
	if m == nil {
		return 0
	}
	// 缓存命中价 > 0 时，从 input 中扣除 cached_tokens，避免重复计费
	nonCachedInput := promptTokens
	if m.CachedPricePerMTokens > 0 && cachedTokens > 0 {
		nonCachedInput -= cachedTokens
		if nonCachedInput < 0 {
			nonCachedInput = 0
		}
	}
	const usdToCents = 100
	inputCost := float64(nonCachedInput) / 1e6 * m.InputPricePerMTokens * usdToCents
	outputCost := float64(completionTokens) / 1e6 * m.OutputPricePerMTokens * usdToCents
	cachedCost := float64(cachedTokens) / 1e6 * m.CachedPricePerMTokens * usdToCents
	return int64(inputCost + outputCost + cachedCost)
}

// sanitizeForLog 先脱敏敏感字段再截断，避免错误日志泄露 API key 等凭据。
// 顺序关键：必须先 redact 后 truncate，否则截断后的内容可能遗漏关键字。
// 截断策略：保留首段（多行错误时取首个换行符之前的内容），便于 ChainError 等
// 聚合错误保留"主模型"信息；末尾追加提示告知还有更多尝试被省略。
func sanitizeForLog(s string) string {
	// 简单关键字脱敏（项目内 LLM 调用通常无此问题，但作为防御性兜底）
	for _, kw := range []string{"api_key", "authorization", "bearer "} {
		if idx := indexCI(s, kw); idx >= 0 {
			// 截断到关键字前并加省略号
			if idx > 64 {
				s = s[:idx] + "...(redacted)"
			} else {
				s = "...(redacted)"
			}
			break
		}
	}
	const maxLen = 512
	if len(s) > maxLen {
		cut := s[:maxLen]
		// 在 maxLen 范围内找最近的换行符，避免切到行中间；找不到则保留首段（首行本身超长）
		if idx := strings.LastIndex(cut, "\n"); idx > 0 {
			cut = cut[:idx]
		}
		s = cut + "\n...(truncated, more attempts omitted)"
	}
	return s
}

func indexCI(s, sub string) int {
	// 简单大小写不敏感查找
	sLow, subLow := []byte(s), []byte(sub)
	for i := 0; i+len(subLow) <= len(sLow); i++ {
		match := true
		for j := 0; j < len(subLow); j++ {
			c1, c2 := sLow[i+j], subLow[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// ChatCompletionStructured 调用大模型并返回结构化响应（含完整 ChatResponse）
// 用于 AI 评审结构化输出场景
// taskID 用于关联 Token 用量日志，nil 表示无任务关联
// caller 用于在 Token 用量日志中标记调用方
func (s *LLMService) ChatCompletionStructured(taskID *uint, modelID uint, caller, systemPrompt, userPrompt string, responseFormat *llm.ResponseFormat) (*StructuredChatResult, error) {
	// ① 用户强制指定了模型 → 直接调用，不走主备
	if modelID > 0 {
		resp, m, err := s.callSpecificModel(taskID, modelID, caller, systemPrompt, userPrompt, responseFormat)
		if err != nil {
			return nil, err
		}
		content := ""
		if len(resp.Choices) > 0 {
			content = resp.Choices[0].Message.Content
		}
		return &StructuredChatResult{Content: content, ModelID: m.ID, ModelName: m.ModelID, Response: resp}, nil
	}

	// ② 未指定 modelID → 走全局主备链路
	resp, m, err := s.tryChain(taskID, responseFormat, caller, systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	content := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
	}
	return &StructuredChatResult{Content: content, ModelID: m.ID, ModelName: m.ModelID, Response: resp}, nil
}
