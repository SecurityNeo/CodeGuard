package handler

import (
	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"strconv"
)

type PoolHandler struct{}

func NewPoolHandler() *PoolHandler {
	return &PoolHandler{}
}

func (h *PoolHandler) List(c *gin.Context) {
	name := c.Query("name")
	pools, err := service.NewPoolService().List(name)
	if err != nil {
		zap.L().Error("list pools failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": pools})
}

func (h *PoolHandler) Get(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	pool, err := service.NewPoolService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, gin.H{"data": pool})
}

func (h *PoolHandler) Create(c *gin.Context) {
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	required := []string{"name", "opencode_endpoint", "max_parallel", "check_interval_sec"}
	for _, key := range required {
		if _, ok := data[key]; !ok {
			c.JSON(400, gin.H{"error": "missing field: " + key})
			return
		}
	}

	pool, err := service.NewPoolService().Create(data)
	if err != nil {
		zap.L().Error("create pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("资源池创建", pool.Name, pool.ID, userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "created", "data": pool})
}

func (h *PoolHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	pool, _ := service.NewPoolService().Get(uint(id))
	if err := service.NewPoolService().Update(uint(id), data); err != nil {
		zap.L().Error("update pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("资源池更新", pool.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "updated"})
}

func (h *PoolHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	pool, _ := service.NewPoolService().Get(uint(id))
	if err := service.NewPoolService().Delete(uint(id)); err != nil {
		zap.L().Error("delete pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("资源池删除", pool.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "deleted"})
}

func (h *PoolHandler) CheckConnectivity(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	connected, msg, version, err := service.NewPoolService().CheckConnectivity(uint(id))
	if err != nil {
		zap.L().Error("check pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if connected {
		c.JSON(200, gin.H{"message": "连接成功", "status": "connected", "version": version})
	} else {
		c.JSON(200, gin.H{"message": "连接失败: " + msg, "status": "error"})
	}
}

func (h *PoolHandler) TestConnectivity(c *gin.Context) {
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	endpoint, _ := data["opencode_endpoint"].(string)
	username, _ := data["opencode_username"].(string)
	password, _ := data["opencode_password"].(string)

	connected, msg, err := service.NewOpencodeService().TestConnectivityByConfig(endpoint, username, password)
	if err != nil {
		zap.L().Error("test connectivity failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if connected {
		c.JSON(200, gin.H{"message": msg, "status": "connected"})
	} else {
		c.JSON(200, gin.H{"message": msg, "status": "error"})
	}
}

func (h *PoolHandler) Toggle(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	enabled, _ := data["enabled"].(bool)
	pool, _ := service.NewPoolService().Get(uint(id))

	if err := service.NewPoolService().Toggle(uint(id), enabled); err != nil {
		zap.L().Error("toggle pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	action := "启用"
	if !enabled {
		action = "禁用"
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("资源池"+action, pool.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "toggled"})
}

func (h *PoolHandler) SetDefault(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	pool, _ := service.NewPoolService().Get(uint(id))
	if err := service.NewPoolService().SetDefault(uint(id)); err != nil {
		zap.L().Error("set default pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("设为默认资源池", pool.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "set as default"})
}

func (h *PoolHandler) GetPoolSkills(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	skills, err := service.NewOpencodeService().GetSkills(uint(id))
	if err != nil {
		zap.L().Error("get pool skills failed", zap.Error(err), zap.Int("pool_id", id))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": skills})
}

func (h *PoolHandler) UnsetDefault(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	pool, _ := service.NewPoolService().Get(uint(id))
	if err := service.NewPoolService().UnsetDefault(uint(id)); err != nil {
		zap.L().Error("unset default pool failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("取消默认资源池", pool.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "unset default"})
}
