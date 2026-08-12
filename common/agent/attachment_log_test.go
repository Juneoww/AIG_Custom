package agent

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/gologger"
)

func TestReachableAgentAttachmentLogsDoNotExposeNamesPathsOrRawErrors(t *testing.T) {
	var output bytes.Buffer
	logger := gologger.StdLogger.Logrus()
	previousOutput := logger.Out
	logger.SetOutput(&output)
	t.Cleanup(func() { logger.SetOutput(previousOutput) })

	attachment := `opaque-secret-file-name.zip`
	localPath := `/private/agent/tmp-secret-report.json`
	rawError := fmt.Errorf(`upload %s from %s failed at C:\private\token-secret.png`, attachment, localPath)

	logAgentAttachmentTransfer("download_started", "session-safe", TaskTypeModelRedteamReport)
	logAgentAttachmentFailure("download_failed", "session-safe", TaskTypeModelRedteamReport, rawError)
	logs := output.String()

	for _, secret := range []string{attachment, localPath, rawError.Error()} {
		if strings.Contains(logs, secret) {
			t.Fatalf("agent attachment log leaked %q: %s", secret, logs)
		}
	}
	for _, allowed := range []string{"download_started", "download_failed", "session-safe", TaskTypeModelRedteamReport} {
		if !strings.Contains(logs, allowed) {
			t.Fatalf("agent attachment log must contain whitelisted field %q: %s", allowed, logs)
		}
	}
}
