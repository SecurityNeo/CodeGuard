package engine

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/ai-optimizer/backend/pkg/llm"
)

// CommentTemplateContext GitLab 评论模板上下文
type CommentTemplateContext struct {
	TaskID              uint
	ProjectName         string
	MRTitle             string
	MRAuthor            string
	TotalScore          int
	Summary             string
	DimensionsTable     string // 预渲染 Markdown 表格
	Dimensions          []DimensionContext
	IssuesList          string // 预渲染 Markdown 列表
	Issues              []IssueContext
	IssueCount          int
	CriticalCount       int
	HighCount           int
	MediumCount         int
	LowCount            int
	InfoCount           int
	Recommendations     []string
	RecommendationsList string // 预渲染
	BR                  string // 换行
}

// DimensionContext 维度上下文
type DimensionContext struct {
	Name        string
	Label       string
	Score       int
	Weight      int
	WeightLabel string
}

// IssueContext Issue 上下文
type IssueContext struct {
	Severity      string
	SeverityLabel string
	SeverityEmoji string
	DeductScore   int
	RuleCode      string
	RuleName      string // 可从 rule_code 映射
	Category      string
	File          string
	LineStart     int
	LineEnd       int
	CodeSnippet   string
	Message       string
	Suggestion    string
}

// DefaultGitLabCommentTemplate 默认 GitLab 评论模板
const DefaultGitLabCommentTemplate = `## 🤖 AI 代码评审报告

{{if gt .CriticalCount 0}}
⚠️ **发现 {{.CriticalCount}} 个严重问题，请立即处理！**
{{end}}

**综合评分：{{.TotalScore}}/100**

### 📊 维度评分
{{.DimensionsTable}}

{{if gt .IssueCount 0}}
### ⚠️ 发现的问题（共 {{.IssueCount}} 个）
{{.IssuesList}}
{{end}}

{{if .Recommendations}}
### 💡 改进建议
{{.RecommendationsList}}
{{end}}
`

// AssembleMarkdownComment 将结构化评审结果组装为 Markdown 评论
func AssembleMarkdownComment(result *llm.AIReviewResult, tmplStr string) (string, error) {
	if tmplStr == "" {
		tmplStr = DefaultGitLabCommentTemplate
	}

	ctx := buildCommentContext(result)

	tmpl, err := template.New("comment").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse comment template failed: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute comment template failed: %w", err)
	}

	return buf.String(), nil
}

// buildCommentContext 构建模板上下文
func buildCommentContext(result *llm.AIReviewResult) *CommentTemplateContext {
	ctx := &CommentTemplateContext{
		TotalScore:      result.TotalScore,
		Summary:         result.Summary,
		IssueCount:      len(result.Issues),
		Recommendations: result.Recommendations,
		BR:              "\n\n",
	}

	// 统计各严重级别数量
	for _, issue := range result.Issues {
		switch issue.Severity {
		case "critical":
			ctx.CriticalCount++
		case "high":
			ctx.HighCount++
		case "medium":
			ctx.MediumCount++
		case "low":
			ctx.LowCount++
		case "info":
			ctx.InfoCount++
		}
	}

	// 构建维度列表
	for name, dim := range result.Dimensions {
		ctx.Dimensions = append(ctx.Dimensions, DimensionContext{
			Name:        name,
			Label:       dimensionLabel(name),
			Score:       dim.Score,
			Weight:      dim.Weight,
			WeightLabel: fmt.Sprintf("%d%%", dim.Weight),
		})
	}

	// 预渲染维度表格
	ctx.DimensionsTable = buildDimensionsTable(ctx.Dimensions)

	// 构建 Issue 列表
	for _, issue := range result.Issues {
		ctx.Issues = append(ctx.Issues, IssueContext{
			Severity:      issue.Severity,
			SeverityLabel: severityLabel(issue.Severity),
			SeverityEmoji: severityEmoji(issue.Severity),
			DeductScore:   issue.DeductScore,
			RuleCode:      issue.RuleCode,
			Category:      issue.Category,
			File:          issue.File,
			LineStart:     issue.LineStart,
			LineEnd:       issue.LineEnd,
			CodeSnippet:   issue.CodeSnippet,
			Message:       issue.Message,
			Suggestion:    issue.Suggestion,
		})
	}

	// 预渲染 Issue 列表
	ctx.IssuesList = buildIssuesList(ctx.Issues)

	// 预渲染改进建议
	ctx.RecommendationsList = buildRecommendationsList(ctx.Recommendations)

	return ctx
}

// buildDimensionsTable 预渲染维度评分 Markdown 表格
func buildDimensionsTable(dims []DimensionContext) string {
	if len(dims) == 0 {
		return "（无维度评分）"
	}
	var b strings.Builder
	b.WriteString("| 维度 | 得分 | 权重 |\n")
	b.WriteString("|------|------|------|\n")
	for _, d := range dims {
		b.WriteString(fmt.Sprintf("| %s | %d | %s |\n", d.Label, d.Score, d.WeightLabel))
	}
	return b.String()
}

// buildIssuesList 预渲染 Issue Markdown 列表
func buildIssuesList(issues []IssueContext) string {
	if len(issues) == 0 {
		return "（未发现明确问题）"
	}

	grouped := groupIssuesBySeverity(issues)
	var b strings.Builder

	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if group, ok := grouped[sev]; ok && len(group) > 0 {
			b.WriteString(fmt.Sprintf("#### %s (%d)\n\n", severitySectionLabel(sev), len(group)))
			for _, issue := range group {
				scoreLabel := ""
				if issue.DeductScore > 0 {
					scoreLabel = fmt.Sprintf("【扣%d分】", issue.DeductScore)
				}
				b.WriteString(fmt.Sprintf("**%s [%s] %s%s**\n\n", issue.SeverityEmoji, issue.RuleCode, scoreLabel, issue.Message))
				if issue.File != "" {
					b.WriteString(fmt.Sprintf("- **文件**：`%s`", issue.File))
					if issue.LineStart > 0 {
						b.WriteString(fmt.Sprintf(" (第 %d", issue.LineStart))
						if issue.LineEnd > issue.LineStart {
							b.WriteString(fmt.Sprintf("-%d", issue.LineEnd))
						}
						b.WriteString(" 行)")
					}
					b.WriteString("\n")
				}
				if issue.CodeSnippet != "" {
					b.WriteString(fmt.Sprintf("```\n%s\n```\n", issue.CodeSnippet))
				}
				if issue.Suggestion != "" {
					b.WriteString(fmt.Sprintf("- **建议**：%s\n", issue.Suggestion))
				}
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

// buildRecommendationsList 预渲染改进建议
func buildRecommendationsList(recommendations []string) string {
	if len(recommendations) == 0 {
		return ""
	}
	var b strings.Builder
	for _, rec := range recommendations {
		b.WriteString(fmt.Sprintf("- %s\n", rec))
	}
	return b.String()
}

// groupIssuesBySeverity 按严重级别分组
func groupIssuesBySeverity(issues []IssueContext) map[string][]IssueContext {
	grouped := make(map[string][]IssueContext)
	for _, issue := range issues {
		grouped[issue.Severity] = append(grouped[issue.Severity], issue)
	}
	return grouped
}

// dimensionLabel 维度名称映射
func dimensionLabel(name string) string {
	labels := map[string]string{
		"security":        "安全性",
		"code_quality":    "代码质量",
		"readability":     "可读性",
		"maintainability": "可维护性",
		"test_coverage":   "测试覆盖",
	}
	if v, ok := labels[name]; ok {
		return v
	}
	return name
}

// severityLabel 严重级别标签
func severityLabel(severity string) string {
	labels := map[string]string{
		"critical": "严重",
		"high":     "高危",
		"medium":   "中危",
		"low":      "低危",
		"info":     "提示",
	}
	if v, ok := labels[severity]; ok {
		return v
	}
	return severity
}

// severityEmoji 严重级别 Emoji
func severityEmoji(severity string) string {
	emojis := map[string]string{
		"critical": "🔴",
		"high":     "🟠",
		"medium":   "🟡",
		"low":      "🔵",
		"info":     "⚪",
	}
	if v, ok := emojis[severity]; ok {
		return v
	}
	return "⚪"
}

// severitySectionLabel 严重级别章节标题
func severitySectionLabel(severity string) string {
	labels := map[string]string{
		"critical": "🔴 严重",
		"high":     "🟠 高危",
		"medium":   "🟡 中危",
		"low":      "🔵 低危",
		"info":     "⚪ 提示",
	}
	if v, ok := labels[severity]; ok {
		return v
	}
	return severity
}
