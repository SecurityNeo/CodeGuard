package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/gitlab"
	"github.com/ai-optimizer/backend/pkg/llm"
)

// DimensionWeight 维度权重配置（JSON 解析用）
type DimensionWeight struct {
	Weight int    `json:"weight"`
	Label  string `json:"label"`
}

// DeductScoreConfig 扣分规则配置
type DeductScoreConfig struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
}

// DefaultDeductScoreConfig 返回默认扣分配置
func DefaultDeductScoreConfig() DeductScoreConfig {
	return DeductScoreConfig{
		Critical: 10,
		High:     5,
		Medium:   3,
		Low:      1,
		Info:     0,
	}
}

// DeductScoreFor 按 severity 返回对应扣分值
func (cfg DeductScoreConfig) DeductScoreFor(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return cfg.Critical
	case "high":
		return cfg.High
	case "medium":
		return cfg.Medium
	case "low":
		return cfg.Low
	case "info":
		return cfg.Info
	default:
		return 0
	}
}

// PromptContext Prompt 组装所需的上下文
type PromptContext struct {
	Files             []gitlab.DiffFile // diff 文件列表
	CommitsText       string
	MRTitle           string
	CustomInstruction string                     // 项目自定义说明
	DimensionWeights  map[string]DimensionWeight // 维度权重
	DeductScoreConfig DeductScoreConfig          // 扣分规则配置
	Rules             []model.ReviewRule         // 启用的规则列表
	MaxRules          int                        // 最多输出规则数
}

// BuildReviewPrompt 组装 AI 评审 Prompt
func BuildReviewPrompt(ctx *PromptContext) string {
	var sb strings.Builder

	// 1. System 角色及输出约束
	sb.WriteString("你是一名资深代码审查专家。请对以下代码变更进行严格审查。\n\n")
	sb.WriteString("## 【重要】返回格式要求\n")
	sb.WriteString("你的响应必须严格符合以下 JSON Schema，不要包含任何 Markdown 代码块标记（如 ```json）或额外解释文字。如果不涉及的评分维度，可以不包含在 dimensions 内。所有字段必须填写，issues 数组为空时填写 []，recommendations 为空时填写 []：\n")

	// 动态生成 SchemaExample，确保示例和实际维度完全一致
	schemaExample := buildSchemaExample(ctx.DimensionWeights)
	sb.WriteString(schemaExample)
	sb.WriteString("\n\n")

	sb.WriteString("## 【总分计算规则 - 必须严格遵守】\n")
	sb.WriteString("1. 每个 Issue 按严重程度固定扣分（deduct_score）：\n")
	dsc := ctx.DeductScoreConfig
	sb.WriteString(fmt.Sprintf("   - critical（严重）: 扣 %d 分\n", dsc.Critical))
	sb.WriteString(fmt.Sprintf("   - high（高危）: 扣 %d 分\n", dsc.High))
	sb.WriteString(fmt.Sprintf("   - medium（中危）: 扣 %d 分\n", dsc.Medium))
	sb.WriteString(fmt.Sprintf("   - low（低危）: 扣 %d 分\n", dsc.Low))
	sb.WriteString(fmt.Sprintf("   - info（提示）: 扣 %d 分\n\n", dsc.Info))
	sb.WriteString("2. 按维度汇总扣分：\n")
	sb.WriteString("   将 issues 按 category 分组，每组内所有 deduct_score 相加。\n\n")
	sb.WriteString("3. 计算各维度得分：\n")
	sb.WriteString("   维度得分 = max(0, 100 - 该维度扣分总和)\n\n")
	sb.WriteString("4. 计算总分（加权平均）：\n")
	sb.WriteString("   total_score = \u03A3(维度得分 \u00D7 维度权重) / 100\n")
	sb.WriteString("   结果四舍五入到整数（0-100）。\n\n")
	sb.WriteString("5. 权重为 0 的维度不参与总分计算，但仍返回 score 供参考。\n\n")
	sb.WriteString("【重要】total_score 必须与上述公式计算结果一致，不能随意填写。\n\n")

	// 2. 项目自定义说明
	if ctx.CustomInstruction != "" {
		sb.WriteString("## 【项目特殊要求】\n")
		sb.WriteString(ctx.CustomInstruction)
		sb.WriteString("\n\n")
	}

	// 3. 维度 + 权重 + 规则 柔和在一起
	sb.WriteString("## 【评分维度、权重及评审规则】\n\n")
	sb.WriteString("请严格按照以下维度和权重进行评分，所有维度得分必须在 0-100 之间，涉及的维度权重之和应为 100：\n\n")

	selected, _ := SelectTopRules(ctx.Rules, ctx.MaxRules)
	grouped := groupRulesByCategory(selected)

	// 维度排序按权重降序，权重相同按名称
	order := sortDimensions(ctx.DimensionWeights)
	for _, cat := range order {
		group, ok := grouped[cat]
		if !ok || len(group) == 0 {
			continue
		}
		weightVal := 0
		if dim, ok2 := ctx.DimensionWeights[cat]; ok2 {
			weightVal = dim.Weight
		}
		sb.WriteString(fmt.Sprintf("### %s（权重 %d%%）：%s\n\n", categoryDisplay(cat), weightVal, cat))
		for _, rule := range group {
			severityLabel := severityDisplay(rule.Severity)
			sb.WriteString(fmt.Sprintf("#### `%s` | %s | **%s**\n\n", rule.Code, rule.Name, severityLabel))

			promptText := strings.TrimSpace(rule.Prompt)
			if promptText != "" {
				hasStructure := strings.HasPrefix(promptText, "#") ||
					strings.HasPrefix(promptText, "-") ||
					strings.HasPrefix(promptText, "*") ||
					listItemRe.MatchString(promptText)
				if !hasStructure {
					sb.WriteString("**检查要点：**\n")
				}
				sb.WriteString(promptText)
				sb.WriteString("\n\n")
			}
		}
	}
	sb.WriteString("对于未在规则列表中的其他问题，也可以一并指出，此时 `rule_code` 填空字符串。\n\n")

	// 4. 待评审代码
	sb.WriteString("【待评审的代码变更】\n")
	for i, file := range ctx.Files {
		if file.Diff == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("### 文件 %d：%s\n", i+1, file.NewPath))
		sb.WriteString("```diff\n")
		sb.WriteString(file.Diff)
		sb.WriteString("\n```\n\n")
	}

	// 5. Commit 信息
	if ctx.CommitsText != "" {
		sb.WriteString(fmt.Sprintf("【Commit 历史】\n%s\n\n", ctx.CommitsText))
	}

	// 6. MR 标题
	sb.WriteString(fmt.Sprintf("【MR 标题】%s\n", ctx.MRTitle))

	return sb.String()
}

// buildSchemaExample 根据实际维度权重动态生成 JSON Schema 示例
func buildSchemaExample(dimWeights map[string]DimensionWeight) string {
	// 从 dimWeights 提取 dimensions 对象
	dims := make(map[string]llm.Dimension)
	for code, dim := range dimWeights {
		dims[code] = llm.Dimension{Score: 85, Weight: dim.Weight}
	}

	ex := struct {
		SchemaVersion   string                   `json:"schema_version"`
		TotalScore      int                      `json:"total_score"`
		Dimensions      map[string]llm.Dimension `json:"dimensions"`
		Summary         string                   `json:"summary"`
		Issues          []struct {
			RuleCode    string `json:"rule_code"`
			Severity    string `json:"severity"`
			Category    string `json:"category"`
			File        string `json:"file"`
			LineStart   int    `json:"line_start"`
			LineEnd     int    `json:"line_end"`
			CodeSnippet string `json:"code_snippet"`
			Message     string `json:"message"`
			Suggestion  string `json:"suggestion"`
		} `json:"issues"`
		Recommendations []string `json:"recommendations"`
	}{
		SchemaVersion: "1.0",
		TotalScore:    85,
		Dimensions:    dims,
		Summary:       "本次MR整体质量良好，但存在若干需要关注的问题。",
		Issues: []struct {
			RuleCode    string `json:"rule_code"`
			Severity    string `json:"severity"`
			Category    string `json:"category"`
			File        string `json:"file"`
			LineStart   int    `json:"line_start"`
			LineEnd     int    `json:"line_end"`
			CodeSnippet string `json:"code_snippet"`
			Message     string `json:"message"`
			Suggestion  string `json:"suggestion"`
		}{
			{
				RuleCode:    "security-hardcoded-secret",
				Severity:    "high",
				Category:    "security",
				File:        "pkg/service/auth.go",
				LineStart:   45,
				LineEnd:     52,
				CodeSnippet: "const API_KEY = \"sk-1234567890abcdef\"",
				Message:     "此处使用了硬编码密钥",
				Suggestion:  "建议改从环境变量读取，如 os.Getenv(\"API_KEY\")",
			},
		},
		Recommendations: []string{"建议增加单元测试覆盖", "考虑将密码学操作抽取为独立包"},
	}

	b, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return SchemaExampleFallback
	}
	return string(b)
}

// sortDimensions 按权重降序排序维度 code，权重相同按名称字母序
func sortDimensions(dimWeights map[string]DimensionWeight) []string {
	type pair struct {
		code   string
		weight int
	}
	pairs := make([]pair, 0, len(dimWeights))
	for code, dim := range dimWeights {
		pairs = append(pairs, pair{code: code, weight: dim.Weight})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].weight != pairs[j].weight {
			return pairs[i].weight > pairs[j].weight
		}
		return pairs[i].code < pairs[j].code
	})
	result := make([]string, len(pairs))
	for i, p := range pairs {
		result[i] = p.code
	}
	return result
}

// listItemRe 匹配有序列表项开头（如 1. 2.）
var listItemRe = regexp.MustCompile(`^\d+\.\s`)

// SelectRulesResult 规则截断结果
type SelectRulesResult struct {
	Selected   []model.ReviewRule // 实际传入Prompt的规则
	Truncated  []model.ReviewRule // 被截断未传入的规则
	TotalCount int                // 总计规则数
}

// SelectTopRules 按严重级别排序并截断规则列表
// 严重级别枚举: critical > high > medium > low > info
func SelectTopRules(rules []model.ReviewRule, max int) (selected []model.ReviewRule, truncated []model.ReviewRule) {
	severityOrder := map[string]int{"critical": 5, "high": 4, "medium": 3, "low": 2, "info": 1}

	sorted := make([]model.ReviewRule, len(rules))
	copy(sorted, rules)

	sort.Slice(sorted, func(i, j int) bool {
		return severityOrder[sorted[i].Severity] > severityOrder[sorted[j].Severity]
	})

	if len(sorted) > max {
		return sorted[:max], sorted[max:]
	}
	return sorted, nil
}

// groupRulesByCategory 按维度类别分组规则
func groupRulesByCategory(rules []model.ReviewRule) map[string][]model.ReviewRule {
	grouped := make(map[string][]model.ReviewRule)
	for _, rule := range rules {
		cat := rule.Category
		if cat == "" {
			cat = "common"
		}
		grouped[cat] = append(grouped[cat], rule)
	}
	return grouped
}

// categoryDisplay 维度名称中文映射（查看 ReviewCategory 表获取最终源）
// 内置 5 个维度硬编码中文；自定义维度直接返回原名
func categoryDisplay(category string) string {
	m := map[string]string{
		"security":        "安全性",
		"performance":     "性能",
		"readability":     "可读性",
		"maintainability": "可维护性",
		"test_coverage":   "测试覆盖",
	}
	if v, ok := m[category]; ok {
		return v
	}
	// 自定义维度：直接返回原名
	return category
}

// severityDisplay 严重级别中文显示
func severityDisplay(severity string) string {
	m := map[string]string{
		"critical": "严重",
		"high":     "高危",
		"medium":   "中危",
		"low":      "低危",
		"info":     "提示",
	}
	if v, ok := m[severity]; ok {
		return v
	}
	return severity
}

// SchemaExampleFallback 兜底示例（无法 marshal 时使用）
const SchemaExampleFallback = `{
  "schema_version": "1.0",
  "total_score": 85,
  "dimensions": {
    "security": {"score": 95, "weight": 30}
  },
  "summary": "本次MR整体质量良好。",
  "issues": [
    {
      "rule_code": "security-hardcoded-secret",
      "severity": "high",
      "category": "security",
      "file": "pkg/service/auth.go",
      "line_start": 45,
      "line_end": 52,
      "code_snippet": "const API_KEY = \"sk-1234567890abcdef\"",
      "message": "此处使用了硬编码密钥",
      "suggestion": "建议改从环境变量读取"
    }
  ],
  "recommendations": ["建议增加单元测试覆盖"]
}`

// ParseDeductScoreConfig 从 JSON 字符串解析扣分规则配置
func ParseDeductScoreConfig(jsonStr string) (DeductScoreConfig, error) {
	cfg := DefaultDeductScoreConfig()
	if jsonStr == "" || jsonStr == "{}" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
