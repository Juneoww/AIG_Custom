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

// @title AI-Infra-Guard 任务API
// @version 1.0
// @description API for managing AI security scanning tasks
// @BasePath /
package websocket

import (
	"embed"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Juneoww/AIG_Custom/common/trpc"
	_ "github.com/Juneoww/AIG_Custom/docs"
	"github.com/Juneoww/AIG_Custom/internal/gologger"
	version "github.com/Juneoww/AIG_Custom/internal/options"
	platformadmin "github.com/Juneoww/AIG_Custom/internal/platform/admin"
	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformknowledge "github.com/Juneoww/AIG_Custom/internal/platform/knowledge"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"trpc.group/trpc-go/trpc-go/log"
)

//go:embed static/*
var staticFS embed.FS

func RunWebServer(options *version.Options) {
	// 1. 初始化trpc-go
	if err := trpc.InitTrpc("./trpc_go.yaml"); err != nil {
		log.Fatalf("Trpc-go初始化失败: %v", err)
	}
	log.Infof("Trpc-go initialized successfully: trace_id=system_startup")

	r := gin.Default()
	// 2. 添加中间件
	//r.Use(middleware.TrpcMiddleware())
	//r.Use(middleware.RequestLoggerMiddleware()) // 添加请求参数日志中间件
	// r.Use(middleware.MetricsMiddleware()) // 移除HTTP监控中间件，依赖TRPC自动监控

	// 3. 初始化数据库和Agentmanager
	dbConfig, err := database.LoadConfigFromEnv() // 从环境变量加载数据库配置
	if err != nil {
		gologger.Fatalf("加载数据库配置失败: %v", err)
	}
	db, err := database.InitDB(dbConfig)
	if err != nil {
		log.Fatalf("数据库初始化失败: trace_id=system_startup, error=%v", err)
	}
	stores, err := initializeRuntimeDatastores(db)
	if err != nil {
		log.Fatalf("初始化运行时数据库失败: trace_id=system_startup, error=%v", err)
	}
	identityRepo := stores.identityRepository
	identityPolicy, err := identity.CookiePolicyFromEnv()
	if err != nil {
		log.Fatalf("身份 Cookie 配置无效: trace_id=system_startup, error=%v", err)
	}
	identityService := identity.NewService(identityRepo)
	auditRepo := stores.auditRepository
	auditService := platformaudit.NewService(auditRepo)
	platformModelRepo := stores.platformModelRepository
	modelKeyring, err := platformmodels.LoadKeyringFromEnv()
	if err != nil {
		log.Fatalf("模型主密钥配置无效: trace_id=system_startup, error=%v", err)
	}
	platformModelService := platformmodels.NewService(platformModelRepo, modelKeyring, auditService)
	adminHandler := platformadmin.NewHandler(identityService, auditService)
	knowledgeService := platformknowledge.NewService(auditService)
	knowledgeHandler := platformknowledge.NewHandler(knowledgeService)
	taskStore := stores.taskStore
	modelStore := stores.modelStore
	// 初始化AgentManager
	agentManager := NewAgentManager()

	// 初始化文件上传配置（支持环境变量）
	fileConfig := LoadFileUploadConfigFromEnv()

	// 验证文件上传配置
	if err := fileConfig.ValidateConfig(); err != nil {
		log.Errorf("文件上传配置验证失败: trace_id=system_startup, error=%v", err)

	}

	// 初始化SSE管理器
	sseManager := NewSSEManager()

	taskManager := NewTaskManager(agentManager, taskStore, modelStore, fileConfig, sseManager)
	taskManager.SetModelResolver(platformmodels.NewScannerResolver(platformModelRepo, identityRepo, modelKeyring))
	err = taskManager.taskStore.ResetRunningTasks()
	if err != nil {
		log.Fatalf("重置运行中的任务失败: %v", err)
	}

	// 将 TaskManager 注入到 AgentManager
	agentManager.SetTaskManager(taskManager)

	// API 版本分组
	v1 := r.Group("/api/v1")
	{
		identity.RegisterRoutesWithObserver(v1.Group("/auth"), identityService, identityPolicy, auditService)
		registerPlatformGovernanceRoutes(v1.Group("/platform"), identityService, identityPolicy, adminHandler, platformModelService)
		v1.GET("/images/:path", func(context *gin.Context) {
			path := context.Param("path")
			if strings.Contains(path, "..") {
				context.String(403, "Forbidden")
				return
			}
			context.File(filepath.Join("uploads", path))
		})
		// 1. 知识库模块
		knowledge := v1.Group("/knowledge")
		knowledge.Use(setupIdentityMiddleware(identityService, identityPolicy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(identityPolicy))
		{
			// AI应用指纹
			fingerprints := knowledge.Group("/fingerprints")
			{
				// 管理功能
				fingerprints.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleListFingerprints)
				fingerprints.POST("", knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationCreate, HandleCreateFingerprint))
				fingerprints.PUT("/:name", knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationUpdate, HandleEditFingerprint))
				fingerprints.DELETE("", knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationDelete, HandleDeleteFingerprint))
			}
			// 漏洞库
			vulnerabilities := knowledge.Group("/vulnerabilities")
			{
				// 管理功能
				vulnerabilities.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleListVulnerabilities())
				vulnerabilities.POST("", knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationCreate, HandleCreateVulnerability()))
				vulnerabilities.PUT("/:cve", knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationUpdate, HandleEditVulnerability))
				vulnerabilities.DELETE("", knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationDelete, HandleBatchDeleteVulnerabilities))
			}
			// 评测集
			evaluations := knowledge.Group("/evaluations")
			{
				// 管理功能
				evaluations.GET("/:name", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleGetEvaluationDetail)
				evaluations.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleListEvaluations)
				evaluations.POST("", knowledgeHandler.Govern(platformknowledge.KindEvaluation, platformknowledge.OperationCreate, HandleCreateEvaluation))
				evaluations.PUT("/:name", knowledgeHandler.Govern(platformknowledge.KindEvaluation, platformknowledge.OperationUpdate, HandleEditEvaluation))
				evaluations.DELETE("", knowledgeHandler.Govern(platformknowledge.KindEvaluation, platformknowledge.OperationDelete, HandleDeleteEvaluation))
			}
			// MCP
			mcp := knowledge.Group("/mcp")
			{
				mcp.GET("names", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), GetMcpPluginList)
				mcp.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleList(MCPROOT, McpLoadFile))
				mcp.POST("", knowledgeHandler.Govern(platformknowledge.KindMCP, platformknowledge.OperationCreate, HandleCreate(mcpReadAndSave)))
				mcp.PUT("/:id", knowledgeHandler.Govern(platformknowledge.KindMCP, platformknowledge.OperationUpdate, HandleEdit(mcpUpdateFunc)))
				mcp.DELETE("/:id", knowledgeHandler.Govern(platformknowledge.KindMCP, platformknowledge.OperationDelete, HandleDelete(mcpDeleteFunc)))
			}
			// Prompt Collections
			collections := knowledge.Group("/prompt_collections")
			{
				collections.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleList(PromptCollectionsRoot, promptCollectionLoadFile))
				collections.POST("", knowledgeHandler.Govern(platformknowledge.KindPromptCollection, platformknowledge.OperationCreate, HandleCreate(promptCollectionReadAndSave)))
				collections.PUT("/:id", knowledgeHandler.Govern(platformknowledge.KindPromptCollection, platformknowledge.OperationUpdate, HandleEdit(promptCollectionUpdateFunc)))
				collections.DELETE("", knowledgeHandler.Govern(platformknowledge.KindPromptCollection, platformknowledge.OperationDelete, HandleDelete(promptCollectionDeleteFunc)))
			}
			agentConfigs := knowledge.Group("/agent")
			{
				agentConfigs.GET("/names", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleListAgentNames)
				agentConfigs.GET("/:name", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleGetAgentConfig)
				agentConfigs.POST("/:name", knowledgeHandler.Govern(platformknowledge.KindAgentConfig, platformknowledge.OperationUpdate, HandleSaveAgentConfig))
				agentConfigs.DELETE("/:name", knowledgeHandler.Govern(platformknowledge.KindAgentConfig, platformknowledge.OperationDelete, HandleDeleteAgentConfig))
				agentConfigs.POST("/connect", identity.RequireRole(identity.RoleAdmin), HandleAgentConnect)
				agentConfigs.POST("/prompt_test", identity.RequireRole(identity.RoleAdmin), HandleAgentPromptTest)
				agentConfigs.GET("/template", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleAgentTemplate)
			}
			// 算子列表
			knowledge.GET("/jailbreak", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), GetJailBreak)
		}
		taskOwnerByID := func(c *gin.Context) string {
			session, err := taskStore.GetSession(c.Param("id"))
			if err != nil {
				return ""
			}
			return session.Username
		}
		appSecurity := v1.Group("/app")
		{
			appSecurity.Use(setupIdentityMiddleware(identityService, identityPolicy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(identityPolicy))
			taskOwner := func(c *gin.Context) string {
				session, err := taskStore.GetSession(c.Param("sessionId"))
				if err != nil {
					return ""
				}
				return session.Username
			}
			// 任务管理
			tasks := appSecurity.Group("/tasks")
			{
				// 获取任务列表接口
				tasks.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), func(c *gin.Context) {
					HandleGetTaskList(c, taskManager)
				})
				// 获取任务详情接口
				tasks.GET("/:sessionId", identity.RequireOwnerOrRole(taskOwner, false), func(c *gin.Context) {
					HandleGetTaskDetail(c, taskManager)
				})
				// 分享任务接口
				tasks.POST("/share", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
					HandleShare(c, taskManager)
				})
				// SSE接口
				tasks.GET("/sse/:sessionId", identity.RequireOwnerOrRole(taskOwner, false), func(c *gin.Context) {
					HandleTaskSSE(c, taskManager)
				})
				// 新建任务接口
				tasks.POST("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
					HandleTaskCreate(c, taskManager)
				})
				// 文件上传接口（完整文件上传）
				tasks.POST("/uploadFile", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
					HandleUploadFile(c, taskManager)
				})
				// 分片上传接口
				tasks.POST("/uploadChunk", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
					HandleUploadFileChunk(c, taskManager)
				})
				// 合并分片接口
				tasks.POST("/mergeChunks", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
					HandleMergeFileChunks(c, taskManager)
				})
				// 文件下载接口
				tasks.POST("/:sessionId/downloadFile", identity.RequireOwnerOrRole(taskOwner, true), func(c *gin.Context) {
					HandleDownloadFile(c, taskManager)
				})
				// 编辑任务接口
				tasks.PUT("/:sessionId", identity.RequireOwnerOrRole(taskOwner, true), func(c *gin.Context) {
					HandleUpdateTask(c, taskManager)
				})
				// 删除任务接口
				tasks.DELETE("/:sessionId", identity.RequireOwnerOrRole(taskOwner, true), func(c *gin.Context) {
					HandleDeleteTask(c, taskManager)
				})
				// 终止任务接口
				tasks.POST("/:sessionId/terminate", identity.RequireOwnerOrRole(taskOwner, true), func(c *gin.Context) {
					HandleTerminateTask(c, taskManager)
				})
			}
			// Deprecated compatibility path. It is intentionally backed by the
			// encrypted platform service and the authenticated Subject, never by
			// the former username-based legacy handlers.
			models := appSecurity.Group("/models")
			registerPlatformModelRoutes(models, platformModelService, modelStore)
		}
		// 4. Agent 管理
		agents := v1.Group("/agents")
		{
			// 只需要WebSocket入口
			agents.GET("/ws", agentManager.HandleAgentWebSocket())
		}
		// 提供给第三方的api
		taskApi := appSecurity.Group("/taskapi")
		{
			// 创建任务
			taskApi.POST("/tasks", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
				SubmitTask(c, taskManager)
			})
			// 获取任务状态
			taskApi.GET("/status/:id", identity.RequireOwnerOrRole(taskOwnerByID, false), func(c *gin.Context) {
				GetTaskStatus(c, taskManager)
			})
			// 获取任务结果
			taskApi.GET("/result/:id", identity.RequireOwnerOrRole(taskOwnerByID, false), func(c *gin.Context) {
				GetTaskResult(c, taskManager)
			})
			taskApi.POST("/upload", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
				HandleUploadFile(c, taskManager)
			})
			// 分片上传接口
			taskApi.POST("/uploadChunk", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
				HandleUploadFileChunk(c, taskManager)
			})
			// 合并分片接口
			taskApi.POST("/mergeChunks", identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) {
				HandleMergeFileChunks(c, taskManager)
			})
		}
		// version
		v1.GET("/version", func(c *gin.Context) {
			filename := "CHANGELOG.md"
			data, err := os.ReadFile(filename)
			if err != nil {
				data = []byte("")
			}
			c.JSON(http.StatusOK, gin.H{
				"version":   version.GetVersion(),
				"changelog": string(data),
			})
		})

		// system — data directory auto-sync & version check
		system := v1.Group("/system")
		system.Use(setupIdentityMiddleware(identityService, identityPolicy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(identityPolicy))
		{
			system.POST("/update-data", knowledgeHandler.GovernAsync(platformknowledge.KindSystemData, platformknowledge.OperationUpdate, HandleTriggerDataUpdate))
			system.GET("/update-data", identity.RequireRole(identity.RoleAdmin, identity.RoleAuditor), HandleGetUpdateStatus)
			system.GET("/version", identity.RequireRole(identity.RoleAdmin, identity.RoleAuditor), HandleVersionCheck)
		}
	}

	// Swagger UI - 必须在 NoRoute 之前注册
	r.GET("/docs/*any", func(c *gin.Context) {
		if c.Request.URL.Path == "/docs/" {
			c.Redirect(302, "/docs/index.html")
		} else {
			ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
		}
	})

	// 静态文件处理
	r.NoRoute(func(c *gin.Context) {
		assetPath := "static" + c.Request.URL.Path
		if c.Request.URL.Path == "/" {
			assetPath = "static/index.html"
		}

		assetData, err := staticFS.ReadFile(assetPath)
		if err != nil {
			assetData, err = staticFS.ReadFile("static/index.html")
			if err != nil {
				c.String(500, "Internal Server Error")
				return
			}
			c.Header("Content-Type", "text/html")
			c.Data(200, "text/html", assetData)
			return
		}

		mimeType := mime.TypeByExtension(filepath.Ext(assetPath))
		if mimeType == "" {
			mimeType = "text/plain"
		}
		c.Header("Content-Type", mimeType)
		c.Data(200, mimeType, assetData)
	})

	log.Infof("Starting WebServer: trace_id=system_startup, addr=%s", options.WebServerAddr)
	if err := r.Run(options.WebServerAddr); err != nil {
		log.Errorf("Could not start WebSocket server: trace_id=system_startup, error=%s", err)
	}
}

// 配置身份认证中间件
func setupIdentityMiddleware(service *identity.Service, policy identity.CookiePolicy) gin.HandlerFunc {
	return identity.Authenticate(service, policy)
}

func registerPlatformGovernanceRoutes(
	group *gin.RouterGroup,
	identityService *identity.Service,
	identityPolicy identity.CookiePolicy,
	adminHandler *platformadmin.Handler,
	modelService *platformmodels.Service,
) {
	group.Use(
		setupIdentityMiddleware(identityService, identityPolicy),
		identity.RequirePasswordChangeCompleted(),
		identity.RequireCSRF(identityPolicy),
	)
	adminHandler.Register(group.Group("/admin"))
	registerGovernanceModelRoutes(group.Group("/models"), modelService)
}
