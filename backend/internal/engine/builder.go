package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/gitlab"
)

// PromptContext Prompt 组装所需的上下文
type PromptContext struct {
	Files             []gitlab.DiffFile // diff 文件列表
	CommitsText       string
	MRTitle           string
	CustomInstruction string                    // 项目自定义说明
	DimensionWeights  map[string]DimensionWeight // 维度权重
	Rules             []model.ReviewRule         // 启用的规则列表
	MaxRules          int                        // 最多输出规则数
}

// DimensionWeight 维度权重配置（JSON 解析用）
type DimensionWeight struct {
	Weight int    `json:"weight"`
	Label  string `json:"label"`
}

// BuildReviewPrompt 组装 AI 评审 Prompt
func BuildReviewPrompt(ctx *PromptContext) string {
	var sb strings.Builder

	// 1. System 角色及输出约束
	sb.WriteString("你是一名资深代码审查专家。请对以下代码变更进行严格审查。\n\n")
	sb.WriteString("【重要】你的响应必须严格符合以下 JSON Schema，不要包含任何 Markdown 代码块标记（如 ```json）或额外解释文字。")
	sb.WriteString("所有字段必须填写，issues 数组为空时填写 []，recommendations 为空时填写 []：\n")
	sb.WriteString(SchemaExample)
	sb.WriteString("\n\n")

	// 2. 项目自定义说明
	if ctx.CustomInstruction != "" {
		sb.WriteString("【项目特殊要求】\n")
		sb.WriteString(ctx.CustomInstruction)
		sb.WriteString("\n\n")
	}

	// 3. 维度评分权重说明
	sb.WriteString("【评分维度及权重】\n")
	sb.WriteString("请严格按照以下维度和权重进行评分，所有维度得分必须在 0-100 之间，权重之和为 100：\n")
	for name, dim := range ctx.DimensionWeights {
		sb.WriteString(fmt.Sprintf("- %s（权重 %d%%）：%s\n", dim.Label, dim.Weight, name))
	}
	sb.WriteString("\n")

	// 4. 评审规则列表（截断到最多 N 条）
	rules := selectTopRules(ctx.Rules, ctx.MaxRules)
	sb.WriteString(fmt.Sprintf("【重点关注以下 %d 条评审规则】\n", len(rules)))
	for i, rule := range rules {
		severityLabel := severityDisplay(rule.Severity)
		sb.WriteString(fmt.Sprintf("%d. [%s] %s（%s %s）：%s\n",
			i+1, rule.Category, rule.Name, severityLabel, rule.Severity, rule.Prompt))
	}
	sb.WriteString("\n对于未在规则列表中的其他问题，也可以一并指出，此时 rule_code 填空字符串。\n\n")

	// 5. 待评审代码
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

	// 6. Commit 信息
	if ctx.CommitsText != "" {
		sb.WriteString(fmt.Sprintf("【Commit 历史】\n%s\n\n", ctx.CommitsText))
	}

	// 7. MR 标题
	sb.WriteString(fmt.Sprintf("【MR 标题】%s\n", ctx.MRTitle))

	return sb.String()
}

// selectTopRules 按严重级别排序并截断规则列表
func selectTopRules(rules []model.ReviewRule, max int) []model.ReviewRule {
	severityOrder := map[string]int{"critical": 5, "high": 4, "medium": 3, "low": 2, "info": 1}

	sorted := make([]model.ReviewRule, len(rules))
	copy(sorted, rules)

	sort.Slice(sorted, func(i, j int) bool {
		return severityOrder[sorted[i].Severity] > severityOrder[sorted[j].Severity]
	})

	if len(sorted) > max {
		return sorted[:max]
	}
	return sorted
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

// SchemaExample 给模型看的 JSON 输出示例
const SchemaExample = `
{
  "schema_version": "1.0",
  "total_score": 85,
  "dimensions": {
    "security": {"score": 95, "weight": 30},
    "code_quality": {"score": 80, "weight": 25},
    "readability": {"score": 75, "weight": 20},
    "maintainability": {"score": 85, "weight": 15},
    "test_coverage": {"score": 90, "weight": 10}
  },
  "summary": "本次MR整体质量良好，但存在2个安全问题需要关注。",
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
      "suggestion": "建议改从环境变量读取，如 os.Getenv(\"API_KEY\")"
    }
  ],
  "recommendations": ["建议增加单元测试覆盖", "考虑将密码学操作抽取为独立包"]
}
`
