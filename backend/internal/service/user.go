package service

import (
	"errors"
	"fmt"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/encrypt"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type UserService struct{}

func NewUserService() *UserService {
	return &UserService{}
}

// InitAdmin 初始化 admin 用户（如果不存在）
func (s *UserService) InitAdmin() error {
	var user model.User
	result := model.DB.Where("username = ?", "admin").First(&user)
	
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("query admin user failed: %w", result.Error)
	}
	
	// 用户已存在
	if result.Error == nil {
		zap.L().Info("admin user already exists")
		return nil
	}
	
	// 创建默认 admin 用户
	hashedPassword, err := encrypt.HashPassword("admin123")
	if err != nil {
		return fmt.Errorf("hash password failed: %w", err)
	}
	admin := model.User{
		Username: "admin",
		Password: hashedPassword,
		Role:     "admin",
	}
	if err := model.DB.Create(&admin).Error; err != nil {
		return fmt.Errorf("create admin user failed: %w", err)
	}
	zap.L().Info("admin user created with default password: admin123")
	return nil
}

// ValidateLogin 验证登录
func (s *UserService) ValidateLogin(username, password string) (*model.User, bool) {
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, false
	}

	if !encrypt.CheckPassword(password, user.Password) {
		return nil, false
	}

	return &user, true
}

// ChangePassword 修改密码
func (s *UserService) ChangePassword(userID uint, oldPassword, newPassword string) error {
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		return fmt.Errorf("user not found")
	}

	// 验证旧密码
	if !encrypt.CheckPassword(oldPassword, user.Password) {
		return fmt.Errorf("旧密码错误")
	}

	// 哈希新密码
	hashedPassword, err := encrypt.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password failed: %w", err)
	}

	// 更新密码
	if err := model.DB.Model(&user).Update("password", hashedPassword).Error; err != nil {
		return fmt.Errorf("update password failed: %w", err)
	}

	return nil
}

// GetByID 根据ID获取用户
func (s *UserService) GetByID(id uint) (*model.User, error) {
	var user model.User
	if err := model.DB.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}
