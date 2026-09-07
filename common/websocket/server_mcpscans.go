package websocket

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpegress"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpscans"
	"github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mcpServerModule struct {
	connections *mcpconnections.Handler
	scans       *mcpscans.Handler
	gateway     *mcpegress.Handler
	archives    *mcpegress.RepositoryArchives
}

// newMCPServerModule 显式装配 MCP 专属域。主密钥与出站白名单只取服务端环境；
// 未配置主密钥时保留安全只读入口，写操作失败关闭，不影响其他扫描模块。
func newMCPServerModule(db *gorm.DB, repository *tasks.GormRepository, taskService *tasks.Service, manager *TaskManager, modelResolver *models.ScannerResolver, audits audit.Recorder, attachmentConfig tasks.AttachmentConfig, listenAddress string) (*mcpServerModule, error) {
	connections := mcpconnections.NewGormRepository(db)
	replayRepository := idempotency.NewGormRepository(db)
	capabilities := mcpegress.NewGormRepository(db)
	for _, initialize := range []func() error{connections.Init, replayRepository.Init, capabilities.Init} {
		if err := initialize(); err != nil {
			return nil, err
		}
	}
	var keyring *mcpconnections.Keyring
	if strings.TrimSpace(os.Getenv(mcpconnections.EnvMasterKey)) != "" || strings.TrimSpace(os.Getenv(mcpconnections.EnvMasterKeyID)) != "" {
		var err error
		keyring, err = mcpconnections.LoadKeyringFromEnv()
		if err != nil {
			return nil, mcpegress.ErrRuntimeUnavailable
		}
	}
	policy, err := mcpconnections.LoadOutboundPolicyFromEnvironment(mcpconnections.OutboundPolicyDependencies{Dialer: &net.Dialer{Timeout: 5 * time.Second}, ControlledDialerAvailable: true})
	if err != nil {
		return nil, err
	}
	port, err := mcpconnections.NewHTTPProbePort(policy, mcpconnections.HTTPProbeOptions{})
	if err != nil {
		return nil, err
	}
	connectionService := mcpconnections.NewService(connections, keyring, mcpconnections.NewProbeEngine(port, mcpconnections.ProbeOptions{}), policy)
	attachmentConfig.MCPOnly = true
	attachments, err := tasks.NewAttachmentService(repository, attachmentConfig, audits)
	if err != nil {
		return nil, err
	}
	taskService.SetMCPAttachmentService(attachments)
	archives := mcpegress.NewRepositoryArchives(repository, attachments, policy)
	gatewayURL, err := mcpGatewayBaseURL(listenAddress)
	if err != nil {
		return nil, err
	}
	runtime := mcpegress.NewService(mcpegress.ServiceDependencies{Tasks: repository, Bindings: connections, Keyring: keyring, Capabilities: capabilities, Policy: policy, Fetcher: archives, GatewayURL: gatewayURL})
	taskService.SetMCPRuntimeIssuer(runtime)
	manager.SetMCPEventRedactor(runtime)
	replay := idempotency.NewService(replayRepository)
	var sourceSealer mcpscans.RepositorySourceSealer
	if keyring != nil {
		sourceSealer = keyring
	}
	workflow := mcpscans.NewCreateUnitOfWork(mcpscans.CreateUnitOfWorkDependencies{Models: modelResolver, Idempotency: replay, Audits: audits, Tasks: taskService, Connections: connectionService, Bindings: connections, Keyring: sourceSealer, Policy: policy, Dispatcher: taskService})
	attachmentAdapter, err := mcpscans.NewAttachmentAdapter(attachments, replay)
	if err != nil {
		return nil, err
	}
	return &mcpServerModule{connections: mcpconnections.NewHandler(connectionService, replay, audits), scans: mcpscans.NewHandler(mcpscans.NewService(workflow, repository, taskService, replay, audits), attachmentAdapter), gateway: mcpegress.NewHandler(mcpegress.NewProxy(mcpegress.ProxyDependencies{Service: runtime})), archives: archives}, nil
}

func mcpGatewayBaseURL(listenAddress string) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("MCP_GATEWAY_BASE_URL")), "/")
	if raw == "" {
		host, port, err := net.SplitHostPort(listenAddress)
		if err != nil {
			return "", mcpegress.ErrRuntimeUnavailable
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		raw = "http://" + net.JoinHostPort(host, port)
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", mcpegress.ErrRuntimeUnavailable
	}
	return raw, nil
}

func (module *mcpServerModule) RegisterPlatform(protected *gin.RouterGroup) {
	module.connections.Register(protected)
	module.scans.Register(protected)
}
func (module *mcpServerModule) RegisterInternal(group *gin.RouterGroup, authenticate gin.HandlerFunc) {
	module.gateway.Register(group, authenticate)
	group.GET("/mcp-archives/:sessionID/:archiveID", authenticate, func(c *gin.Context) {
		if module.archives == nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		data, err := module.archives.ReadArchive(c.Request.Context(), c.Param("sessionID"), "archive:"+c.Param("archiveID"))
		if err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Disposition", "attachment; filename=source.zip")
		c.Data(http.StatusOK, "application/zip", data)
	})
}
