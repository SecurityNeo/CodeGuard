package handler

import "github.com/gin-gonic/gin"

type GitLabAuthHandler struct{}

func NewGitLabAuthHandler() *GitLabAuthHandler {
	return &GitLabAuthHandler{}
}

func (h *GitLabAuthHandler) Redirect(c *gin.Context) {
	c.JSON(501, gin.H{"error": "not implemented"})
}

func (h *GitLabAuthHandler) Callback(c *gin.Context) {
	c.JSON(501, gin.H{"error": "not implemented"})
}
