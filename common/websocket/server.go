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
	"context"
	"embed"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/common/trpc"
	_ "github.com/Juneoww/AIG_Custom/internal/apidocs"
	"github.com/Juneoww/AIG_Custom/internal/gologger"
	version "github.com/Juneoww/AIG_Custom/internal/options"
	platformadmin "github.com/Juneoww/AIG_Custom/internal/platform/admin"
	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	platformbrand "github.com/Juneoww/AIG_Custom/internal/platform/brand"
	platformdashboard "github.com/Juneoww/AIG_Custom/internal/platform/dashboard"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformknowledge "github.com/Juneoww/AIG_Custom/internal/platform/knowledge"
	platformmcpworkbench "github.com/Juneoww/AIG_Custom/internal/platform/mcpworkbench"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	platformreports "github.com/Juneoww/AIG_Custom/internal/platform/reports"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"trpc.group/trpc-go/trpc-go/log"
)

//go:embed static/*
var staticFS embed.FS

const (
	consoleIndexFile             = "static/index.html"
	consoleDocumentCacheControl  = "no-cache"
	consoleImmutableCacheControl = "public, max-age=31536000, immutable"
)

func registerEmbeddedStaticRoutes(router *gin.Engine) {
	router.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}

		requestPath := c.Request.URL.Path
		if isReservedConsoleRoute(requestPath) {
			c.Status(http.StatusNotFound)
			return
		}

		assetPath, validPath := embeddedConsoleAssetPath(requestPath)
		if !validPath {
			c.Status(http.StatusNotFound)
			return
		}

		if assetData, err := staticFS.ReadFile(assetPath); err == nil {
			serveEmbeddedConsoleAsset(c, assetPath, assetData)
			return
		}

		if isStaticAssetRequest(requestPath) {
			c.Status(http.StatusNotFound)
			return
		}

		indexData, err := staticFS.ReadFile(consoleIndexFile)
		if err != nil {
			c.String(http.StatusInternalServerError, "Internal Server Error")
			return
		}
		c.Header("Cache-Control", consoleDocumentCacheControl)
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexData)
	})
}

func isReservedConsoleRoute(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") || requestPath == "/legacy" || strings.HasPrefix(requestPath, "/legacy/")
}

func embeddedConsoleAssetPath(requestPath string) (string, bool) {
	if requestPath == "/" {
		return consoleIndexFile, true
	}
	if !strings.HasPrefix(requestPath, "/") || strings.Contains(requestPath, "..") {
		return "", false
	}

	cleanedPath := path.Clean(requestPath)
	if !strings.HasPrefix(cleanedPath, "/") {
		return "", false
	}
	return "static" + cleanedPath, true
}

func isStaticAssetRequest(requestPath string) bool {
	return strings.HasPrefix(requestPath, "/assets/") || strings.HasPrefix(requestPath, "/fonts/") || strings.HasPrefix(requestPath, "/licenses/") || path.Ext(requestPath) != ""
}

func serveEmbeddedConsoleAsset(c *gin.Context, assetPath string, assetData []byte) {
	contentType := mime.TypeByExtension(path.Ext(assetPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if isImmutableConsoleAsset(assetPath) {
		c.Header("Cache-Control", consoleImmutableCacheControl)
	} else {
		c.Header("Cache-Control", consoleDocumentCacheControl)
	}
	c.Data(http.StatusOK, contentType, assetData)
}

func isImmutableConsoleAsset(assetPath string) bool {
	extension := strings.ToLower(path.Ext(assetPath))
	if strings.HasPrefix(assetPath, "static/fonts/") {
		return extension == ".ttf" || extension == ".woff" || extension == ".woff2"
	}
	return strings.HasPrefix(assetPath, "static/assets/") && (extension == ".css" || extension == ".js")
}

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
	internalAgentToken, err := LoadInternalAgentTokenFromEnv()
	if err != nil {
		log.Fatalf("内部 Agent 认证配置无效: trace_id=system_startup, error=%v", err)
	}
	// 初始化AgentManager
	agentManager := NewAgentManager(internalAgentToken)

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
	attachmentConfig, err := platformtasks.LoadAttachmentConfigFromEnv(fileConfig.UploadDir)
	if err != nil {
		log.Fatalf("附件大小配置无效: trace_id=system_startup, error=%v", err)
	}
	attachmentService, err := platformtasks.NewAttachmentService(stores.platformTaskRepository, attachmentConfig, auditService)
	if err != nil {
		log.Fatalf("初始化私有附件服务失败: trace_id=system_startup, error=%v", err)
	}
	platformTaskService := platformtasks.NewService(stores.platformTaskRepository, taskManager, auditService)
	platformTaskService.SetAttachmentService(attachmentService)
	brandService := platformbrand.NewGovernedService(stores.brandRepository, auditService)
	reportRenderer, err := platformreports.NewEmbeddedPDFRenderer()
	if err != nil {
		log.Fatalf("初始化报告 PDF 渲染器失败: trace_id=system_startup")
	}
	reportService := platformreports.NewGovernedService(
		stores.reportRepository, brandService, auditService, reportRenderer, platformTaskService,
	)
	platformTaskService.SetReportSnapshotService(reportService)
	taskManager.SetPlatformTaskEventSink(platformTaskService)
	platformTaskHandler := platformtasks.NewHandler(platformTaskService, attachmentService)
	reportHandler := platformreports.NewHandler(reportService)
	dashboardHandler := platformdashboard.NewHandler(platformdashboard.NewService(reportService, platformTaskService))
	mcpWorkbenchHandler := platformmcpworkbench.NewHandler(platformmcpworkbench.NewService(reportService, platformTaskService))
	brandHandler := platformbrand.NewHandler(brandService)
	err = taskManager.taskStore.ResetRunningTasks()
	if err != nil {
		log.Fatalf("重置运行中的任务失败: %v", err)
	}
	go startCompletedResultRecovery(context.Background(), 30*time.Second, newRecoveryTicker, platformTaskService.ReconcileCompletedEngineResults)

	// 将 TaskManager 注入到 AgentManager
	agentManager.SetTaskManager(taskManager)

	// API 版本分组
	v1 := r.Group("/api/v1")
	{
		registerPublicRoutes(v1, brandService)
		auth := v1.Group("/auth")
		identity.RegisterRoutesWithObserver(auth, identityService, identityPolicy, auditService)
		platformGroup := v1.Group("/platform")
		registerPlatformGovernanceRoutes(platformGroup, identityService, identityPolicy, adminHandler, platformModelService, platformTaskHandler)
		registerPlatformDashboardRoutes(platformGroup, dashboardHandler)
		registerPlatformMCPWorkbenchRoutes(platformGroup, mcpWorkbenchHandler)
		registerPlatformReportRoutes(platformGroup, reportHandler, brandHandler)
		// 1. 知识库模块
		knowledge := v1.Group("/knowledge")
		knowledge.Use(setupIdentityMiddleware(identityService, identityPolicy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(identityPolicy))
		{
			// AI应用指纹
			fingerprints := knowledge.Group("/fingerprints")
			{
				// 管理功能
				fingerprints.GET("/:name/raw", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleGetFingerprintRaw)
				fingerprints.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleListFingerprints)
				fingerprints.POST("", knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationCreate, HandleCreateFingerprint))
				fingerprints.PUT("/:name", knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationUpdate, HandleEditFingerprint))
				fingerprints.DELETE("", knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationDelete, HandleDeleteFingerprint))
			}
			// 漏洞库
			vulnerabilities := knowledge.Group("/vulnerabilities")
			{
				// 管理功能
				vulnerabilities.GET("/:id/raw", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleGetVulnerabilityRaw)
				vulnerabilities.GET("", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleListVulnerabilities())
				vulnerabilities.POST("", knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationCreate, HandleCreateVulnerability()))
				vulnerabilities.PUT("/:cve", knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationUpdate, HandleEditVulnerability))
				vulnerabilities.DELETE("", knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationDelete, HandleBatchDeleteVulnerabilities))
			}
			// 评测集
			evaluations := knowledge.Group("/evaluations")
			{
				// 管理功能
				evaluations.GET("/:name/raw", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor), HandleGetEvaluationRaw)
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
				collections.DELETE("/:id", knowledgeHandler.Govern(platformknowledge.KindPromptCollection, platformknowledge.OperationDelete, HandleDelete(promptCollectionDeleteFunc)))
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
		v1.POST("/app/tasks/:sessionId/downloadFile", agentManager.RequireInternalToken(), func(c *gin.Context) {
			HandleInternalTaskDownload(c, taskManager)
		})
		v1.POST("/app/tasks/:sessionId/uploadFile", agentManager.RequireInternalToken(), func(c *gin.Context) {
			HandleInternalTaskUpload(c, attachmentService)
		})
		appSecurity := v1.Group("/app")
		{
			appSecurity.Use(setupIdentityMiddleware(identityService, identityPolicy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(identityPolicy))
			registerRetiredBrowserTaskRoutes(appSecurity)
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
	registerEmbeddedStaticRoutes(r)

	log.Infof("Starting WebServer: trace_id=system_startup, addr=%s", options.WebServerAddr)
	if err := r.Run(options.WebServerAddr); err != nil {
		log.Errorf("Could not start WebSocket server: trace_id=system_startup, error=%s", err)
	}
}

// 配置身份认证中间件
func setupIdentityMiddleware(service *identity.Service, policy identity.CookiePolicy) gin.HandlerFunc {
	return identity.Authenticate(service, policy)
}

func registerPublicRoutes(group *gin.RouterGroup, brandService *platformbrand.Service) {
	public := group.Group("/public")
	public.GET("/brand", func(c *gin.Context) {
		view, err := brandService.GetPublic(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "brand request failed"})
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.GET("/version", newSafeVersionHandler(linkerBuildInfo()))
}

func registerPlatformGovernanceRoutes(
	group *gin.RouterGroup,
	identityService *identity.Service,
	identityPolicy identity.CookiePolicy,
	adminHandler *platformadmin.Handler,
	modelService *platformmodels.Service,
	taskHandlers ...*platformtasks.Handler,
) {
	group.Use(
		setupIdentityMiddleware(identityService, identityPolicy),
		identity.RequirePasswordChangeCompleted(),
		identity.RequireCSRF(identityPolicy),
	)
	adminHandler.Register(group.Group("/admin"))
	registerGovernanceModelRoutes(group.Group("/models"), modelService)
	if len(taskHandlers) > 0 && taskHandlers[0] != nil {
		taskHandlers[0].Register(group.Group("/tasks"))
	}
}

func registerPlatformDashboardRoutes(group *gin.RouterGroup, handler *platformdashboard.Handler) {
	if handler != nil {
		handler.Register(group)
	}
}

func registerPlatformMCPWorkbenchRoutes(group *gin.RouterGroup, handler *platformmcpworkbench.Handler) {
	if handler != nil {
		handler.Register(group)
	}
}

func registerPlatformReportRoutes(group *gin.RouterGroup, reportHandler *platformreports.Handler, brandHandler *platformbrand.Handler) {
	if reportHandler != nil {
		reportHandler.Register(group)
	}
	if brandHandler != nil {
		brandHandler.Register(group)
	}
}

func runCompletedResultRecovery(ctx context.Context, ticks <-chan time.Time, reconcile func(context.Context) error) {
	if reconcile == nil {
		return
	}
	run := func() {
		if err := reconcile(ctx); err != nil {
			log.Warnf("平台任务结果恢复未完全完成: trace_id=system_startup")
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			run()
		}
	}
}

type recoveryTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type standardRecoveryTicker struct{ *time.Ticker }

func (ticker standardRecoveryTicker) Chan() <-chan time.Time { return ticker.C }

func newRecoveryTicker(interval time.Duration) recoveryTicker {
	return standardRecoveryTicker{Ticker: time.NewTicker(interval)}
}

func startCompletedResultRecovery(ctx context.Context, interval time.Duration, newTicker func(time.Duration) recoveryTicker, reconcile func(context.Context) error) {
	if newTicker == nil {
		return
	}
	ticker := newTicker(interval)
	if ticker == nil {
		return
	}
	defer ticker.Stop()
	runCompletedResultRecovery(ctx, ticker.Chan(), reconcile)
}
