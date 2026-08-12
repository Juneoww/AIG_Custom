package websocket

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunWebServerComposesProtectedReportingServices(t *testing.T) {
	source, err := os.ReadFile("server.go")
	require.NoError(t, err)
	text := string(source)

	assert.Contains(t, text, "platformreports.NewEmbeddedPDFRenderer()")
	assert.Contains(t, text, "platformreports.NewGovernedService(")
	assert.Contains(t, text, "platformbrand.NewGovernedService(")
	assert.Contains(t, text, "platformTaskService.SetReportSnapshotService(reportService)")
	assert.Contains(t, text, "registerPlatformReportRoutes(platformGroup")
	assert.Less(t, strings.Index(text, "registerPlatformGovernanceRoutes(platformGroup"), strings.Index(text, "registerPlatformReportRoutes(platformGroup"), "report routes must join the same already-protected platform group")
}
