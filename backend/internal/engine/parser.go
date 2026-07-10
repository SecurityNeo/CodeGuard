package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ai-optimizer/backend/pkg/llm"
	"go.uber.org/zap"
)

// RetryConfig JSON 重试配置
type RetryConfig struct {
	MaxAttempts       int
	InitialDelay      time.Duration
	BackoffMultiplier float64
	MaxDelay          time.Duration
	FallbackStrategy  string // "regex" | "markdown" | "fail"
}

// ParseReviewResult 解析 LLM 响应，含重试和 fallback
func ParseReviewResult(
	content string,
	callLLM func() (string, error),
	cfg *RetryConfig,
) (*llm.AIReviewResult, error) {
	// Step 1: 尝试直接解析
	result, err := tryParseJSON(content)
	if err == nil {
		return result, nil
	}
	zap.L().Warn("initial JSON parse failed", zap.Error(err))

	// Step 2: Sanitize 后重试解析
	cleaned := sanitizeJSON(content)
	result, err = tryParseJSON(cleaned)
	if err == nil {
		return result, nil
	}
	zap.L().Warn("sanitized JSON parse failed", zap.Error(err))

	// Step 3: 重试调用 LLM
	if cfg != nil && callLLM != nil {
		delay := cfg.InitialDelay
		for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
			zap.L().Info("retrying LLM call", zap.Int("attempt", attempt), zap.Duration("delay", delay))
			time.Sleep(delay)

			newContent, callErr := callLLM()
			if callErr != nil {
				zap.L().Warn("LLM retry call failed", zap.Int("attempt", attempt), zap.Error(callErr))
				delay = nextDelay(delay, cfg)
				continue
			}

			result, err = tryParseJSON(newContent)
			if err == nil {
				return result, nil
			}

			// sanitize 后再试
			result, err = tryParseJSON(sanitizeJSON(newContent))
			if err == nil {
				return result, nil
			}

			delay = nextDelay(delay, cfg)
		}
	}

	// Step 4: Fallback 策略
	if cfg == nil {
		cfg = &RetryConfig{FallbackStrategy: "fail"}
	}

	zap.L().Warn("all parse attempts failed, using fallback",
		zap.String("strategy", cfg.FallbackStrategy))

	switch cfg.FallbackStrategy {
	case "regex":
		return fallbackRegex(content), nil
	case "markdown":
		return fallbackMarkdown(content), nil
	case "fail":
		return nil, fmt.Errorf("JSON parse failed after all retries: %w", err)
	default:
		return fallbackRegex(content), nil
	}
}

// tryParseJSON 尝试解析 JSON
func tryParseJSON(content string) (*llm.AIReviewResult, error) {
	var result llm.AIReviewResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, err
	}
	// 基础校验
	if result.TotalScore < 0 || result.TotalScore > 100 {
		return nil, fmt.Errorf("invalid total_score: %d", result.TotalScore)
	}
	// 如果 issues 为 nil，初始化为空切片
	if result.Issues == nil {
		result.Issues = []llm.AIReviewIssue{}
	}
	if result.Recommendations == nil {
		result.Recommendations = []string{}
	}
	return &result, nil
}

// sanitizeJSON 清洗可能的 JSON 包装
func sanitizeJSON(raw string) string {
	// 1. 去掉 BOM
	raw = strings.TrimPrefix(raw, "\ufeff")

	// 2. 去掉 markdown 代码块标记
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// 3. 找第一个 { 和最后一个 }
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		raw = raw[start : end+1]
	}

	return strings.TrimSpace(raw)
}

// nextDelay 计算下一次延迟
func nextDelay(current time.Duration, cfg *RetryConfig) time.Duration {
	d := time.Duration(float64(current) * cfg.BackoffMultiplier)
	if d > cfg.MaxDelay {
		return cfg.MaxDelay
	}
	return d
}

// fallbackRegex 正则提取关键信息（兼容旧模式）
func fallbackRegex(content string) *llm.AIReviewResult {
	score := extractScoreFromText(content)
	return &llm.AIReviewResult{
		SchemaVersion:   "1.0",
		TotalScore:      score,
		Dimensions:      map[string]llm.Dimension{},
		Summary:         "AI评审返回非结构化数据，仅提取到总分。",
		Issues:          []llm.AIReviewIssue{},
		Recommendations: []string{},
	}
}

// fallbackMarkdown 保留原始文本但不提取分数
func fallbackMarkdown(content string) *llm.AIReviewResult {
	return &llm.AIReviewResult{
		SchemaVersion:   "1.0",
		TotalScore:      0,
		Dimensions:      map[string]llm.Dimension{},
		Summary:         "AI评审返回原始文本（结构化解析失败）。",
		Issues:          []llm.AIReviewIssue{},
		Recommendations: []string{},
	}
}

// extractScoreFromText 从文本中提取分数
func extractScoreFromText(text string) int {
	re := regexp.MustCompile(`(?i)AI评分[:：]\s*(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		var score int
		fmt.Sscanf(matches[1], "%d", &score)
		return score
	}
	return 0
}

// ParseDimensionWeights 解析模板维度权重 JSON
func ParseDimensionWeights(jsonStr string) (map[string]DimensionWeight, error) {
	if jsonStr == "" {
		// 返回默认权重
		return DefaultDimensionWeights(), nil
	}
	var weights map[string]DimensionWeight
	if err := json.Unmarshal([]byte(jsonStr), &weights); err != nil {
		return DefaultDimensionWeights(), err
	}
	return weights, nil
}

// DefaultDimensionWeights 返回默认维度权重
func DefaultDimensionWeights() map[string]DimensionWeight {
	return map[string]DimensionWeight{
		"security":        {Weight: 30, Label: "安全性"},
		"code_quality":    {Weight: 25, Label: "代码质量"},
		"readability":     {Weight: 20, Label: "可读性"},
		"maintainability": {Weight: 15, Label: "可维护性"},
		"test_coverage":   {Weight: 10, Label: "测试覆盖"},
	}
}
