package handler

import (
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type DashboardHandler struct {
	startTime time.Time
}

func NewDashboardHandler() *DashboardHandler {
	return &DashboardHandler{
		startTime: time.Now(),
	}
}

func (h *DashboardHandler) GetStats(c *gin.Context) {
	var totalProjects, todayTasks, runningTasks, failedTasks24h, activePools int64

	model.DB.Model(&model.Project{}).Count(&totalProjects)

	today := time.Now().Truncate(24 * time.Hour)
	model.DB.Model(&model.Task{}).Where("created_at >= ?", today).Count(&todayTasks)

	model.DB.Model(&model.Task{}).Where("status = ?", model.TaskRunning).Count(&runningTasks)

	yesterday := today.Add(-24 * time.Hour)
	model.DB.Model(&model.Task{}).Where("status = ? AND updated_at >= ?", model.TaskFailed, yesterday).Count(&failedTasks24h)

	model.DB.Model(&model.ResourcePool{}).Where("status = ?", "active").Count(&activePools)

	zap.L().Info("dashboard stats",
		zap.Int64("total_projects", totalProjects),
		zap.Int64("today_tasks", todayTasks),
		zap.Int64("running_tasks", runningTasks),
		zap.Int64("failed_tasks_24h", failedTasks24h),
		zap.Int64("active_pools", activePools),
	)

	c.JSON(200, gin.H{
		"total_projects":   totalProjects,
		"today_tasks":      todayTasks,
		"running_tasks":    runningTasks,
		"failed_tasks_24h": failedTasks24h,
		"active_pools":     activePools,
	})
}

func (h *DashboardHandler) GetTrends(c *gin.Context) {
	labels := make([]string, 7)
	success := make([]int, 7)
	failed := make([]int, 7)

	now := time.Now()
	for i := 6; i >= 0; i-- {
		date := now.AddDate(0, 0, -i)
		labels[6-i] = date.Format("01-02")

		startOfDay := date.Truncate(24 * time.Hour)
		endOfDay := startOfDay.Add(24 * time.Hour)

		var successCount, failedCount int64
		model.DB.Model(&model.Task{}).
			Where("status = ? AND created_at >= ? AND created_at < ?", model.TaskSuccess, startOfDay, endOfDay).
			Count(&successCount)
		model.DB.Model(&model.Task{}).
			Where("status = ? AND created_at >= ? AND created_at < ?", model.TaskFailed, startOfDay, endOfDay).
			Count(&failedCount)

		success[6-i] = int(successCount)
		failed[6-i] = int(failedCount)
	}

	c.JSON(200, gin.H{
		"labels":  labels,
		"success": success,
		"failed":  failed,
	})
}

func (h *DashboardHandler) GetRecentProjects(c *gin.Context) {
	var projects []model.Project

	err := model.DB.Preload("Tasks", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC").Limit(5)
	}).Order("updated_at DESC").Limit(5).Find(&projects).Error

	if err != nil {
		zap.L().Error("get recent projects failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	type ProjectTask struct {
		ID     uint   `json:"id"`
		Name   string `json:"name"`
		Path   string `json:"path"`
		Status string `json:"status"`
	}

	type RecentProject struct {
		ID          uint           `json:"id"`
		Name        string         `json:"name"`
		ProjectPath string         `json:"project_path"`
		Tasks       []ProjectTask  `json:"tasks"`
	}

	result := make([]RecentProject, 0, len(projects))
	for _, p := range projects {
		tasks := make([]ProjectTask, 0, len(p.Tasks))
		for _, t := range p.Tasks {
			tasks = append(tasks, ProjectTask{
				ID:     t.ID,
				Name:   p.Name,
				Path:   p.ProjectPath,
				Status: string(t.Status),
			})
		}
		result = append(result, RecentProject{
			ID:          p.ID,
			Name:        p.Name,
			ProjectPath: p.ProjectPath,
			Tasks:       tasks,
		})
	}

	c.JSON(200, gin.H{"data": result})
}

func (h *DashboardHandler) GetRecentFailures(c *gin.Context) {
	var tasks []model.Task

	err := model.DB.Preload("Project").
		Where("status = ?", model.TaskFailed).
		Order("updated_at DESC").
		Limit(10).
		Find(&tasks).Error

	if err != nil {
		zap.L().Error("get recent failures failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	type FailureTask struct {
		ID          uint   `json:"id"`
		ProjectName string `json:"project_name"`
		MRMergeID   int    `json:"mr_iid"`
		ErrorMsg    string `json:"error_msg"`
		UpdatedAt   string `json:"updated_at"`
	}

	result := make([]FailureTask, 0, len(tasks))
	for _, t := range tasks {
		projectName := t.Project.Name
		if projectName == "" {
			projectName = "未知项目"
		}
		errorMsg := t.ErrorMsg
		if errorMsg == "" {
			errorMsg = "任务执行失败"
		}
		// 截取错误信息前50字符
		if len(errorMsg) > 50 {
			errorMsg = errorMsg[:50] + "..."
		}

		result = append(result, FailureTask{
			ID:          t.ID,
			ProjectName: projectName,
			MRMergeID:   t.MRMergeID,
			ErrorMsg:    errorMsg,
			UpdatedAt:   t.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	c.JSON(200, gin.H{"data": result})
}

func (h *DashboardHandler) GetTaskDistribution(c *gin.Context) {
	type ProjectTaskCount struct {
		ProjectName string
		Count       int64
	}

	var counts []ProjectTaskCount
	model.DB.Model(&model.Task{}).
		Select("projects.name as project_name, COUNT(tasks.id) as count").
		Joins("LEFT JOIN projects ON tasks.project_id = projects.id").
		Group("tasks.project_id").
		Order("count DESC").
		Limit(6).
		Scan(&counts)

	labels := make([]string, len(counts))
	data := make([]int, len(counts))
	colors := []string{"#3b82f6", "#f59e0b", "#8b5cf6", "#10b981", "#ec4899", "#6b7280"}

	for i, c := range counts {
		labels[i] = c.ProjectName
		if labels[i] == "" {
			labels[i] = "未知项目"
		}
		data[i] = int(c.Count)
	}

	// 如果不足6个，添加"其他"
	var otherCount int64
	model.DB.Model(&model.Task{}).Count(&otherCount)
	sum := int64(0)
	for _, d := range data {
		sum += int64(d)
	}
	if otherCount > sum {
		labels = append(labels, "其他")
		data = append(data, int(otherCount-sum))
		colors = append(colors, "#9ca3af")
	}

	c.JSON(200, gin.H{
		"labels": labels,
		"data":   data,
		"colors": colors[:len(labels)],
	})
}