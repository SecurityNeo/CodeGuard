package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/llm"
	"go.uber.org/zap"
)

type LLMService struct{}

func NewLLMService() *LLMService {
	return &LLMService{}
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

// ChatCompletion 直接调用大模型 Chat Completion API
// modelID: 指定模型 ID
//   - modelID > 0: 强制使用指定模型，失败直接报错（不走主备）
//   - modelID == 0: 走全局主备链路（主模型 → 备用1 → 备用2...）
func (s *LLMService) ChatCompletion(modelID uint, systemPrompt, userPrompt string) (*ChatResult, error) {
	// ① 用户强制指定了模型 → 直接调用，不走主备
	if modelID > 0 {
		var m model.LLMModel
		if err := model.DB.First(&m, modelID).Error; err != nil {
			return nil, fmt.Errorf("指定的模型不存在: %w", err)
		}
		if m.Status != "active" {
			return nil, fmt.Errorf("指定的模型[%s]当前状态异常: %s", m.ModelID, m.Status)
		}
		content, err := s.callLLMAPI(&m, systemPrompt, userPrompt, nil)
		if err != nil {
			return nil, fmt.Errorf("指定模型[%s]调用失败: %w", m.ModelID, err)
		}
		return &ChatResult{Content: content.Choices[0].Message.Content, ModelID: m.ID, ModelName: m.ModelID}, nil
	}

	// ② 未指定 modelID → 走全局主备链路
	//   先查主模型
	var primary model.LLMModel
	if err := model.DB.Where("is_primary = ? AND status = ?", true, "active").First(&primary).Error; err == nil {
		resp, err := s.callLLMAPI(&primary, systemPrompt, userPrompt, nil)
		if err == nil {
			zap.L().Info("主模型调用成功",
				zap.Uint("model_id", primary.ID),
				zap.String("model", primary.ModelID))
			return &ChatResult{Content: resp.Choices[0].Message.Content, ModelID: primary.ID, ModelName: primary.ModelID}, nil
		}
		zap.L().Warn("主模型调用失败，准备切换备用",
			zap.Uint("model_id", primary.ID),
			zap.String("model", primary.ModelID),
			zap.Error(err))
	} else {
		zap.L().Warn("未找到可用的主模型，直接尝试备用模型", zap.Error(err))
	}

	// 主模型失败或无主模型 → 按 backup_order 遍历备用
	var backups []model.LLMModel
	model.DB.Where("backup_order > 0 AND status = ?", "active").Order("backup_order ASC").Find(&backups)

	for i, b := range backups {
		resp, err := s.callLLMAPI(&b, systemPrompt, userPrompt, nil)
		if err == nil {
			zap.L().Info("备用模型调用成功",
				zap.Int("backup_index", i+1),
				zap.Uint("model_id", b.ID),
				zap.String("model", b.ModelID))
			return &ChatResult{Content: resp.Choices[0].Message.Content, ModelID: b.ID, ModelName: b.ModelID}, nil
		}
		zap.L().Warn("备用模型调用失败，继续下一个",
			zap.Int("backup_index", i+1),
			zap.Uint("model_id", b.ID),
			zap.String("model", b.ModelID),
			zap.Error(err))
	}

	return nil, fmt.Errorf("主模型和所有备用模型均不可用")
}

// callLLMAPI 实际发起 HTTP 调用（内部辅助函数）
// responseFormat 为 nil 时使用普通文本输出
func (s *LLMService) callLLMAPI(llmModel *model.LLMModel, systemPrompt, userPrompt string, responseFormat *llm.ResponseFormat) (*llm.ChatResponse, error) {
	zap.L().Info("calling LLM API",
		zap.Uint("model_id", llmModel.ID),
		zap.String("provider", llmModel.Provider),
		zap.String("model", llmModel.ModelID))

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
	var sysCfg model.SystemConfig
	timeoutSec := llmModel.TimeoutSec
	if err := model.DB.First(&sysCfg).Error; err == nil && sysCfg.TaskTimeoutMin > 0 {
		timeoutSec = sysCfg.TaskTimeoutMin * 60
		zap.L().Info("llm call using system task_timeout_min",
			zap.Int("timeout_sec", timeoutSec),
			zap.Int("task_timeout_min", sysCfg.TaskTimeoutMin))
	} else {
		zap.L().Warn("llm call using model default timeout",
			zap.Int("timeout_sec", timeoutSec),
			zap.Int("llm_model_timeout_sec", llmModel.TimeoutSec),
			zap.Error(err))
	}

	client := &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API error: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	var result llm.ChatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse LLM response failed: %w", err)
	}

	return &result, nil
}

// ChatCompletionStructured 调用大模型并返回结构化响应（含完整 ChatResponse）
// 用于 AI 评审结构化输出场景
func (s *LLMService) ChatCompletionStructured(modelID uint, systemPrompt, userPrompt string, responseFormat *llm.ResponseFormat) (*StructuredChatResult, error) {
	// ① 用户强制指定了模型 → 直接调用，不走主备
	if modelID > 0 {
		var m model.LLMModel
		if err := model.DB.First(&m, modelID).Error; err != nil {
			return nil, fmt.Errorf("指定的模型不存在: %w", err)
		}
		if m.Status != "active" {
			return nil, fmt.Errorf("指定的模型[%s]当前状态异常: %s", m.ModelID, m.Status)
		}
		resp, err := s.callLLMAPI(&m, systemPrompt, userPrompt, responseFormat)
		if err != nil {
			return nil, fmt.Errorf("指定模型[%s]调用失败: %w", m.ModelID, err)
		}
		content := ""
		if len(resp.Choices) > 0 {
			content = resp.Choices[0].Message.Content
		}
		return &StructuredChatResult{Content: content, ModelID: m.ID, ModelName: m.ModelID, Response: resp}, nil
	}

	// ② 未指定 modelID → 走全局主备链路
	var primary model.LLMModel
	if err := model.DB.Where("is_primary = ? AND status = ?", true, "active").First(&primary).Error; err == nil {
		resp, err := s.callLLMAPI(&primary, systemPrompt, userPrompt, responseFormat)
		if err == nil {
			zap.L().Info("主模型结构化调用成功",
				zap.Uint("model_id", primary.ID),
				zap.String("model", primary.ModelID))
			content := ""
			if len(resp.Choices) > 0 {
				content = resp.Choices[0].Message.Content
			}
			return &StructuredChatResult{Content: content, ModelID: primary.ID, ModelName: primary.ModelID, Response: resp}, nil
		}
		zap.L().Warn("主模型结构化调用失败，准备切换备用",
			zap.Uint("model_id", primary.ID),
			zap.String("model", primary.ModelID),
			zap.Error(err))
	} else {
		zap.L().Warn("未找到可用的主模型，直接尝试备用模型", zap.Error(err))
	}

	// 主模型失败或无主模型 → 按 backup_order 遍历备用
	var backups []model.LLMModel
	model.DB.Where("backup_order > 0 AND status = ?", "active").Order("backup_order ASC").Find(&backups)

	for i, b := range backups {
		resp, err := s.callLLMAPI(&b, systemPrompt, userPrompt, responseFormat)
		if err == nil {
			zap.L().Info("备用模型结构化调用成功",
				zap.Int("backup_index", i+1),
				zap.Uint("model_id", b.ID),
				zap.String("model", b.ModelID))
			content := ""
			if len(resp.Choices) > 0 {
				content = resp.Choices[0].Message.Content
			}
			return &StructuredChatResult{Content: content, ModelID: b.ID, ModelName: b.ModelID, Response: resp}, nil
		}
		zap.L().Warn("备用模型结构化调用失败，继续下一个",
			zap.Int("backup_index", i+1),
			zap.Uint("model_id", b.ID),
			zap.String("model", b.ModelID),
			zap.Error(err))
	}

	return nil, fmt.Errorf("主模型和所有备用模型均不可用")
}
