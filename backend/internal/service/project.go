package service

import (
	"fmt"

	"github.com/ai-optimizer/backend/internal/model"
)

type ProjectService struct{}

func NewProjectService() *ProjectService {
	return &ProjectService{}
}

func (s *ProjectService) List(page, pageSize int, keyword, status, source string) ([]model.Project, int64, error) {
	var projects []model.Project
	var total int64

	db := model.DB.Model(&model.Project{})
	if keyword != "" {
		db = db.Where("name LIKE ? OR project_path LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if status == "active" {
		db = db.Where("ai_enabled = ?", true)
	} else if status == "inactive" {
		db = db.Where("ai_enabled = ?", false)
	}
	if source != "" {
		db = db.Where("source = ?", source)
	}

	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := db.Scopes(model.Paginate(page, pageSize)).
		Preload("Template").
		Preload("Pool").
		Preload("Model").
		Order("updated_at DESC").
		Find(&projects).Error; err != nil {
		return nil, 0, err
	}

	for i := range projects {
		var tasks []model.Task
		model.DB.Where("project_id = ?", projects[i].ID).
			Order("created_at DESC").
			Limit(5).
			Find(&tasks)
		projects[i].Tasks = tasks
	}

	return projects, total, nil
}

func (s *ProjectService) Get(id uint) (*model.Project, error) {
	var p model.Project
	if err := model.DB.Preload("Template").Preload("Pool").Preload("Model").First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *ProjectService) Update(id uint, fields map[string]interface{}) error {
	if v, ok := fields["default_model_id"]; ok && v == nil {
		// 设为 NULL 表示清空默认模型
		delete(fields, "default_model_id")
		return model.DB.Model(&model.Project{}).Where("id = ?", id).
			UpdateColumn("default_model_id", nil).Error
	}
	return model.DB.Model(&model.Project{}).Where("id = ?", id).Updates(fields).Error
}

func (s *ProjectService) Create(data *model.Project) error {
	// Disable foreign key checks for template_id
	model.DB.Exec("SET FOREIGN_KEY_CHECKS=0")
	defer model.DB.Exec("SET FOREIGN_KEY_CHECKS=1")
	return model.DB.Create(data).Error
}

func (s *ProjectService) Delete(id uint) error {
	// 检查是否有运行中任务
	var count int64
	model.DB.Model(&model.Task{}).Where("project_id = ? AND status = ?", id, model.TaskRunning).Count(&count)
	if count > 0 {
		return fmt.Errorf("存在运行中的任务，无法删除")
	}
	return model.DB.Delete(&model.Project{}, id).Error
}

func (s *ProjectService) GetProjectTasks(projectID uint, page, pageSize int) ([]model.Task, int64, error) {
	var tasks []model.Task
	var total int64

	db := model.DB.Where("project_id = ?", projectID)
	if err := db.Model(&model.Task{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Scopes(model.Paginate(page, pageSize)).Order("created_at DESC").Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// Options 返回项目名称列表（用于下拉框选择）
type ProjectOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

func (s *ProjectService) Options() ([]ProjectOption, error) {
	var opts []ProjectOption
	if err := model.DB.Model(&model.Project{}).
		Select("id", "name").
		Order("name ASC").
		Find(&opts).Error; err != nil {
		return nil, err
	}
	return opts, nil
}
