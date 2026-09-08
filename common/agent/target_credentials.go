package agent

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Juneoww/AIG_Custom/common/runner"
	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/Juneoww/AIG_Custom/common/utils/models"
	"github.com/Juneoww/AIG_Custom/internal/options"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
	"github.com/Juneoww/AIG_Custom/pkg/vulstruct"
)

// 认证扫描固定使用受控 Agent 的本地规则，不借用浏览器知识库会话或旧 API Key。
func configureTargetCredentialRules(opts *options.Options, language string) error {
	if opts.TargetAuth == nil {
		return nil
	}
	dataRoot, err := utils.ResolveInfrastructureDataDir()
	if err != nil {
		return fmt.Errorf("基础设施本地规则初始化失败: %w", err)
	}
	opts.FPTemplates = filepath.Join(dataRoot, "fingerprints")
	opts.AdvTemplates = filepath.Join(dataRoot, "vuln")
	opts.Language = language
	opts.LoadRemote = false
	return nil
}

// 在线 Chromium 截图没有受治理认证与跳转边界；凭据模式只使用已脱敏的 HTTP 证据。
func captureInfrastructureVisualEvidence(auth *httpx.TargetAuth, target, response, language string, model *models.OpenAI) ([]byte, *vulstruct.Info, string, error) {
	if auth != nil {
		return nil, nil, "", nil
	}
	if model != nil {
		return runner.Analysis(target, response, language, model)
	}
	screenshot, err := runner.ScreenShot(target)
	return screenshot, nil, "", err
}

const TargetCredentialCapability = "infra-target-auth-v2"

func validateScanTargetAuth(request TaskRequest, scan ScanRequest) error {
	invalid := errors.New("基础设施目标认证配置无效")
	if scan.TargetAuth == nil {
		if scan.TargetCredentialID != "" || scan.TargetCredentialRevision != 0 {
			return invalid
		}
		return nil
	}
	if len(request.Attachments) > 0 || len(scan.Headers) > 0 || scan.TargetAuth.Validate() != nil || scan.TargetAuth.CredentialID != scan.TargetCredentialID || scan.TargetAuth.Revision != scan.TargetCredentialRevision {
		return invalid
	}
	if httpx.ValidateTargetURLs(scan.TargetAuth.Origin, scan.Target, scan.TargetAuth.AllowInsecureHTTP) != nil {
		return invalid
	}
	return nil
}
