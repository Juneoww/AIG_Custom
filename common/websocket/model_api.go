// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Requirement: Any integration or derivative work must explicitly attribute
// Tencent Zhuque Lab (https://github.com/Tencent/AI-Infra-Guard) in its
// documentation or user interface, as detailed in the NOTICE file.

package websocket

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Juneoww/AIG_Custom/common/utils/models"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"trpc.group/trpc-go/trpc-go/log"
)

// registerPlatformModelRoutes exposes the encrypted platform model service.
// The legacy handlers below remain available only as an internal engine
// compatibility implementation and are no longer mounted for browsers.
func registerPlatformModelRoutes(group *gin.RouterGroup, service *platformmodels.Service) {
	group.GET("", func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		views, err := service.List(c.Request.Context(), subject)
		if err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.JSON(http.StatusOK, views)
	})
	group.GET("/:modelID", func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		view, err := service.Get(c.Request.Context(), subject, c.Param("modelID"))
		if err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.POST("", func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		var input platformmodels.CreateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		view, err := service.Create(c.Request.Context(), subject, input)
		if err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.JSON(http.StatusCreated, view)
	})
	group.PUT("/:modelID", func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		var input platformmodels.UpdateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		view, err := service.Update(c.Request.Context(), subject, c.Param("modelID"), input)
		if err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.DELETE("/:modelID", func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		if err := service.Delete(c.Request.Context(), subject, c.Param("modelID")); err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/:modelID/rotate-encryption", func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		if err := service.RotateEncryption(c.Request.Context(), subject, c.Param("modelID")); err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func respondPlatformModelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, platformmodels.ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, platformmodels.ErrNotFound):
		c.Status(http.StatusNotFound)
	case errors.Is(err, platformmodels.ErrInvalid):
		c.Status(http.StatusBadRequest)
	default:
		c.Status(http.StatusInternalServerError)
	}
}

// ModelInfo 模型信息（用于创建）
type ModelInfo struct {
	Model   string `json:"model" binding:"required"`
	Token   string `json:"token" binding:"required"`
	BaseURL string `json:"base_url" binding:"required"`
	Limit   int    `json:"limit"`
	Note    string `json:"note"`
}

// CreateModelRequest 创建模型请求
type CreateModelRequest struct {
	ModelID string    `json:"model_id" binding:"required"`
	Model   ModelInfo `json:"model" binding:"required"`
}

// UpdateModelInfo 模型信息（用于更新）
// 这里不对 Token/BaseURL 使用 binding:"required"，以支持“只改名称等字段”的场景。
type UpdateModelInfo struct {
	Model   string `json:"model"`
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
	Limit   int    `json:"limit"`
	Note    string `json:"note"`
}

// UpdateModelRequest 更新模型请求
type UpdateModelRequest struct {
	Model UpdateModelInfo `json:"model" binding:"required"`
}

// DeleteModelRequest 删除模型请求
type DeleteModelRequest struct {
	ModelIDs []string `json:"model_ids" binding:"required"`
}

// ModelManager 模型管理器
type ModelManager struct {
	modelStore *database.ModelStore
}

const maskedToken = "********"

// maskToken 用于在对外返回模型信息时隐藏真实的 Token。
// 仅用于 JSON 返回，不影响数据库中实际存储的 Token。
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	return maskedToken
}

// NewModelManager 创建新的ModelManager实例
func NewModelManager(modelStore *database.ModelStore) *ModelManager {
	return &ModelManager{
		modelStore: modelStore,
	}
}

func (mm *ModelManager) modelsForSubject(subject identity.Subject) ([]*database.Model, error) {
	if identity.HasAnyRole(subject, identity.RoleAdmin, identity.RoleAuditor) {
		models, err := mm.modelStore.GetAllModels()
		if err != nil {
			return nil, err
		}
		yamlModels, _ := mm.modelStore.LoadYamlModels()
		return append(models, yamlModels...), nil
	}
	if subject.Role == identity.RoleUser {
		return mm.modelStore.GetUserModels(subject.Username)
	}
	return nil, errResourceAccessDenied
}

func (mm *ModelManager) modelForSubject(subject identity.Subject, modelID string, write bool) (*database.Model, error) {
	model, err := mm.modelStore.GetModel(modelID)
	if err != nil {
		return nil, err
	}
	if err := authorizeResource(subject, model.Username, write); err != nil {
		return nil, err
	}
	return model, nil
}

// HandleGetModelList 获取模型列表接口
func HandleGetModelList(c *gin.Context, mm *ModelManager) {
	traceID := getTraceID(c)
	subject, ok := requestSubject(c)
	if !ok {
		return
	}
	username := subject.Username

	log.Debugf("用户请求获取模型列表: trace_id=%s, username=%s", traceID, username)

	var userModels []*database.Model
	var err error

	userModels, err = mm.modelsForSubject(subject)
	if err != nil {
		if respondResourceAccessDenied(c, err) {
			return
		}
		log.Errorf("获取用户模型列表失败: trace_id=%s, username=%s, error=%v", traceID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "获取模型列表失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	// 转换为期望的返回格式
	var result []map[string]interface{}

	for _, model := range userModels {
		item := map[string]interface{}{
			"model_id": model.ModelID,
			"model": map[string]interface{}{
				"model": model.ModelName,
				// 对外返回时也对用户模型的 token 进行掩码处理
				"token":    maskToken(model.Token),
				"base_url": model.BaseURL,
				"note":     model.Note,
				"limit":    model.Limit,
			},
		}
		if model.Default != nil {
			item["default"] = model.Default
		}
		result = append(result, item)
	}

	log.Debugf("获取模型列表成功: trace_id=%s, username=%s, userModels=%d, publicModels=%d, total=%d",
		traceID, username, len(userModels), len(userModels), len(result))

	c.JSON(http.StatusOK, gin.H{
		"status":  0,
		"message": "获取模型列表成功",
		"data":    result,
	})
}

// HandleGetModelDetail 获取模型详情接口
func HandleGetModelDetail(c *gin.Context, mm *ModelManager) {
	traceID := getTraceID(c)
	modelID := c.Param("modelId")
	subject, ok := requestSubject(c)
	if !ok {
		return
	}
	username := subject.Username

	// 1. 字段校验
	if modelID == "" {
		log.Errorf("模型ID为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型ID不能为空",
			"data":    nil,
		})
		return
	}

	log.Debugf("用户请求获取模型详情: trace_id=%s, modelID=%s, username=%s", traceID, modelID, username)

	// 2. 获取模型信息
	model, err := mm.modelForSubject(subject, modelID, false)
	if err != nil {
		if respondResourceAccessDenied(c, err) {
			return
		}
		log.Errorf("获取模型详情失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, modelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型不存在",
			"data":    nil,
		})
		return
	}

	log.Debugf("获取模型详情成功: trace_id=%s, modelID=%s, username=%s", traceID, modelID, username)

	// 转换为期望的返回格式
	result := map[string]interface{}{
		"model_id": model.ModelID,
		"model": map[string]interface{}{
			"model": model.ModelName,
			// 对外隐藏真实 token，前端如需修改，只能输入新 token
			"token":    maskToken(model.Token),
			"base_url": model.BaseURL,
			"note":     model.Note,
			"limit":    model.Limit,
		},
		"default": model.Default,
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  0,
		"message": "获取模型详情成功",
		"data":    result,
	})
}

// HandleCreateModel 创建模型接口
func HandleCreateModel(c *gin.Context, mm *ModelManager) {
	traceID := getTraceID(c)
	subject, ok := requestSubject(c)
	if !ok {
		return
	}
	if err := authorizeResource(subject, subject.Username, true); err != nil {
		respondResourceAccessDenied(c, err)
		return
	}
	username := subject.Username

	// 1. 字段校验
	var req CreateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("请求参数解析失败: trace_id=%s, username=%s, error=%v", traceID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "请求参数错误: " + err.Error(),
			"data":    nil,
		})
		return
	}

	// 2. 验证必填字段
	if req.ModelID == "" {
		log.Errorf("模型ID为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型ID不能为空",
			"data":    nil,
		})
		return
	}

	if req.Model.Model == "" {
		log.Errorf("模型名称为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型名称不能为空",
			"data":    nil,
		})
		return
	}

	if req.Model.Token == "" {
		log.Errorf("API Token为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "API Token不能为空",
			"data":    nil,
		})
		return
	}

	if req.Model.BaseURL == "" {
		log.Errorf("基础URL为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "基础URL不能为空",
			"data":    nil,
		})
		return
	}
	if req.Model.Limit == 0 {
		req.Model.Limit = 1000
	}

	log.Debugf("用户请求创建模型: trace_id=%s, modelID=%s, modelName=%s, username=%s", traceID, req.ModelID, req.Model.Model, username)

	// 3. 检查模型是否已存在
	exists, err := mm.modelStore.CheckModelExists(req.ModelID)
	if err != nil {
		log.Errorf("检查模型是否存在失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, req.ModelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "检查模型失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	if exists {
		log.Errorf("模型已存在: trace_id=%s, modelID=%s, username=%s", traceID, req.ModelID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型ID已存在",
			"data":    nil,
		})
		return
	}
	// 校验模型 token base_url
	ai := &models.OpenAI{
		Key:     req.Model.Token,
		Model:   req.Model.Model,
		BaseUrl: req.Model.BaseURL,
	}
	if !strings.HasSuffix(ai.BaseUrl, "/") {
		ai.BaseUrl += "/"
	}
	err = ai.Vaild(context.Background())
	if err != nil {
		log.Errorf("模型校验失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, req.ModelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型校验失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	// 4. 创建模型
	model := &database.Model{
		ModelID:   req.ModelID,
		Username:  username,
		ModelName: req.Model.Model,
		Token:     req.Model.Token,
		BaseURL:   req.Model.BaseURL,
		Note:      req.Model.Note,
		Limit:     req.Model.Limit,
	}

	err = mm.modelStore.CreateModel(model)
	if err != nil {
		log.Errorf("创建模型失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, req.ModelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "创建模型失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	log.Debugf("创建模型成功: trace_id=%s, modelID=%s, modelName=%s, username=%s", traceID, req.ModelID, req.Model.Model, username)

	c.JSON(http.StatusOK, gin.H{
		"status":  0,
		"message": "模型创建成功",
		"data":    nil,
	})
}

// HandleUpdateModel 更新模型接口
func HandleUpdateModel(c *gin.Context, mm *ModelManager) {
	traceID := getTraceID(c)
	modelID := c.Param("modelId")
	subject, ok := requestSubject(c)
	if !ok {
		return
	}
	username := subject.Username

	// 1. 字段校验
	if modelID == "" {
		log.Errorf("模型ID为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型ID不能为空",
			"data":    nil,
		})
		return
	}

	var req UpdateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("请求参数解析失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, modelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "请求参数错误: " + err.Error(),
			"data":    nil,
		})
		return
	}

	log.Infof("用户请求更新模型: trace_id=%s, modelID=%s, username=%s", traceID, modelID, username)

	_, err := mm.modelForSubject(subject, modelID, true)
	if err != nil {
		if respondResourceAccessDenied(c, err) {
			return
		}
		log.Errorf("检查模型权限失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, modelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "检查模型权限失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	// 3. 构造更新字段
	// 支持“只改模型名称/备注，不改 key/base_url”的场景：
	// - 前端在编辑时如果不填写 token/base_url，则保持数据库中的原值不变；
	// - 只有在显式传入新 token/base_url 且不等于掩码串时才会更新。
	updates := map[string]interface{}{
		"model_name": req.Model.Model,
		"note":       req.Model.Note,
		"limit":      req.Model.Limit,
	}
	if req.Model.Token != "" && req.Model.Token != maskedToken {
		updates["token"] = req.Model.Token
	}
	if req.Model.BaseURL != "" {
		updates["base_url"] = req.Model.BaseURL
	}

	err = mm.modelStore.UpdateModelByID(modelID, updates)
	if err != nil {
		log.Errorf("更新模型失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, modelID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "更新模型失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	log.Infof("更新模型成功: trace_id=%s, modelID=%s, username=%s", traceID, modelID, username)

	c.JSON(http.StatusOK, gin.H{
		"status":  0,
		"message": "模型更新成功",
		"data":    nil,
	})
}

// HandleDeleteModel 删除模型接口（支持单个和批量）
func HandleDeleteModel(c *gin.Context, mm *ModelManager) {
	traceID := getTraceID(c)
	subject, ok := requestSubject(c)
	if !ok {
		return
	}
	username := subject.Username

	// 1. 字段校验
	var req DeleteModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("请求参数解析失败: trace_id=%s, username=%s, error=%v", traceID, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "请求参数错误: " + err.Error(),
			"data":    nil,
		})
		return
	}

	if len(req.ModelIDs) == 0 {
		log.Errorf("模型ID列表为空: trace_id=%s, username=%s", traceID, username)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "模型ID列表不能为空",
			"data":    nil,
		})
		return
	}

	log.Infof("用户请求删除模型: trace_id=%s, modelIDs=%v, username=%s", traceID, req.ModelIDs, username)

	for _, modelID := range req.ModelIDs {
		_, err := mm.modelForSubject(subject, modelID, true)
		if err != nil {
			if respondResourceAccessDenied(c, err) {
				return
			}
			log.Errorf("检查模型权限失败: trace_id=%s, modelID=%s, username=%s, error=%v", traceID, modelID, username, err)
			c.JSON(http.StatusOK, gin.H{
				"status":  1,
				"message": "检查模型权限失败: " + err.Error(),
				"data":    nil,
			})
			return
		}

	}

	// 3. 批量删除模型
	deletedCount, err := mm.modelStore.BatchDeleteModelsByID(req.ModelIDs)
	if err != nil {
		log.Errorf("删除模型失败: trace_id=%s, modelIDs=%v, username=%s, error=%v", traceID, req.ModelIDs, username, err)
		c.JSON(http.StatusOK, gin.H{
			"status":  1,
			"message": "删除模型失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	log.Infof("删除模型成功: trace_id=%s, modelIDs=%v, username=%s, deletedCount=%d", traceID, req.ModelIDs, username, deletedCount)

	c.JSON(http.StatusOK, gin.H{
		"status":  0,
		"message": "删除成功",
		"data":    nil,
	})
}
