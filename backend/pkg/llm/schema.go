package llm

// AIReviewResult LLM 结构化评审输出（Strict Mode，全字段必填）
// 注意：strict=true 时所有字段均为 required，不能省略
type AIReviewResult struct {
	SchemaVersion   string                  `json:"schema_version"`
	TotalScore      int                     `json:"total_score"`
	Dimensions      map[string]Dimension    `json:"dimensions"`
	Summary         string                  `json:"summary"`
	Issues          []AIReviewIssue         `json:"issues"`
	Recommendations []string                `json:"recommendations"`
}

// Dimension 维度评分
type Dimension struct {
	Score  int `json:"score"`
	Weight int `json:"weight"`
}

// AIReviewIssue 评审发现的 Issue
type AIReviewIssue struct {
	RuleCode    string `json:"rule_code"`    // 为空字符串表示不属于已知规则
	Severity    string `json:"severity"`     // critical/high/medium/low/info
	Category    string `json:"category"`     // 维度分类
	File        string `json:"file"`         // 文件路径
	LineStart   int    `json:"line_start"`   // 起始行号，不确定时为 0
	LineEnd     int    `json:"line_end"`     // 结束行号，单行为 0
	CodeSnippet string `json:"code_snippet"` // 相关代码片段
	Message     string `json:"message"`      // 问题描述
	Suggestion  string `json:"suggestion"`   // 改进建议
}

// GetReviewJSONSchema 返回手写 JSON Schema（替代 invopop/jsonschema，避免外部依赖）
func GetReviewJSONSchema() interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schema_version", "total_score", "dimensions", "summary", "issues", "recommendations"},
		"properties": map[string]interface{}{
			"schema_version": map[string]interface{}{
				"type":        "string",
				"description": "Schema 版本，固定为 1.0",
			},
			"total_score": map[string]interface{}{
				"type":        "integer",
				"description": "综合评分 0-100",
				"minimum":     0,
				"maximum":     100,
			},
			"dimensions": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"security", "code_quality", "readability", "maintainability", "test_coverage"},
				"properties": map[string]interface{}{
					"security": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"score", "weight"},
						"properties": map[string]interface{}{
							"score":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
							"weight": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
						},
					},
					"code_quality": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"score", "weight"},
						"properties": map[string]interface{}{
							"score":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
							"weight": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
						},
					},
					"readability": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"score", "weight"},
						"properties": map[string]interface{}{
							"score":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
							"weight": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
						},
					},
					"maintainability": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"score", "weight"},
						"properties": map[string]interface{}{
							"score":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
							"weight": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
						},
					},
					"test_coverage": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"score", "weight"},
						"properties": map[string]interface{}{
							"score":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
							"weight": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
						},
					},
				},
			},
			"summary": map[string]interface{}{
				"type":        "string",
				"description": "评审总结，100字以内",
			},
			"issues": map[string]interface{}{
				"type":        "array",
				"description": "发现的问题列表，无问题填 []",
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"rule_code", "severity", "category", "file", "line_start", "line_end", "code_snippet", "message", "suggestion"},
					"properties": map[string]interface{}{
						"rule_code": map[string]interface{}{
							"type":        "string",
							"description": "规则编码，不属于已知规则填空字符串",
						},
						"severity": map[string]interface{}{
							"type":        "string",
							"description": "严重级别：critical/high/medium/low/info",
							"enum":        []string{"critical", "high", "medium", "low", "info"},
						},
						"category": map[string]interface{}{
							"type":        "string",
							"description": "所属维度",
							"enum":        []string{"security", "performance", "readability", "maintainability", "test_coverage"},
						},
						"file": map[string]interface{}{
							"type":        "string",
							"description": "文件路径",
						},
						"line_start": map[string]interface{}{
							"type":        "integer",
							"description": "起始行号，不确定时填 0",
						},
						"line_end": map[string]interface{}{
							"type":        "integer",
							"description": "结束行号，单行为 0",
						},
						"code_snippet": map[string]interface{}{
							"type":        "string",
							"description": "相关代码片段",
						},
						"message": map[string]interface{}{
							"type":        "string",
							"description": "问题描述",
						},
						"suggestion": map[string]interface{}{
							"type":        "string",
							"description": "改进建议",
						},
					},
				},
			},
			"recommendations": map[string]interface{}{
				"type":        "array",
				"description": "改进建议列表，无建议填 []",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
	}
}
