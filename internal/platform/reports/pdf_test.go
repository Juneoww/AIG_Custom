package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/signintech/gopdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/gofont/goregular"
)

func TestPDFRendererEmbedsUnicodeFontAndRendersSnapshotSummary(t *testing.T) {
	font := loadReportFont(t)
	renderer, err := NewPDFRenderer(font)
	require.NoError(t, err)
	snapshot := reportPDFSnapshot(t, "image/png", onePixelPNG())
	pdf, err := renderer.Render(context.Background(), snapshot)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(pdf, []byte("%PDF")))
	assert.GreaterOrEqual(t, bytes.Count(pdf, []byte("/FontFile2")), 2)
	assert.GreaterOrEqual(t, bytes.Count(pdf, []byte("/ToUnicode")), 2)
	assert.True(t, bytes.Contains(pdf, []byte("/Image")))
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	for _, heading := range []string{"评分说明", "风险趋势", "Top 风险", "优先处置建议", "扫描覆盖范围", "技术发现"} {
		assert.True(t, slices.ContainsFunc(reportLines(render), func(line string) bool { return strings.Contains(line, heading) }), heading)
	}
}

func TestPDFRendererWrapsAndPaginatesLongTechnicalFindings(t *testing.T) {
	renderer, err := NewPDFRenderer(loadReportFont(t))
	require.NoError(t, err)
	results := make([]map[string]any, 0, 50)
	for index := 0; index < 50; index++ {
		results = append(results, map[string]any{
			"title": fmt.Sprintf("technical-%02d", index), "description": strings.Repeat("evidence ", 10),
			"risk_type": "security", "level": "high", "suggestion": fmt.Sprintf("final-remediation-%02d %s", index, strings.Repeat("repair ", 5)),
		})
	}
	payload, err := json.Marshal(map[string]any{"score": 50, "results": results})
	require.NoError(t, err)
	completed := time.Date(2026, 8, 12, 18, 30, 0, 0, time.UTC)
	snapshot, err := BuildSnapshotAt("long-pdf", "alice", "Mcp-Scan", event(string(payload)), brand.Config{
		ProductName: strings.Repeat("企业安全平台", 30), PrimaryColor: "#1677FF", Watermark: strings.Repeat("内部", 80),
	}, completed, completed)
	require.NoError(t, err)
	snapshot.ID = "long-report"

	pdf, err := renderer.Render(context.Background(), snapshot)
	require.NoError(t, err)
	assert.Greater(t, bytes.Count(pdf, []byte("/Type /Page")), 2, "long findings must produce multiple pages")
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	assert.Contains(t, reportLines(render)[len(reportLines(render))-1], "final-remediation-49", "the final finding must remain in the paginated layout input")
}

func TestPDFRendererProducesPopplerReadableMultiPageUnicodeDocument(t *testing.T) {
	for _, executable := range []string{"pdfinfo", "pdffonts", "pdftotext", "pdftoppm"} {
		if _, err := exec.LookPath(executable); err != nil {
			if os.Getenv("AIG_REQUIRE_POPPLER") == "1" {
				t.Fatalf("%s is required when AIG_REQUIRE_POPPLER=1", executable)
			}
			t.Skipf("%s is required for the PDF interoperability smoke test", executable)
		}
	}
	renderer, err := NewPDFRenderer(loadReportFont(t))
	require.NoError(t, err)
	results := make([]map[string]any, 0, 50)
	for index := 0; index < 50; index++ {
		title := fmt.Sprintf("technical-%02d", index)
		suggestion := fmt.Sprintf("remediation-%02d", index)
		if index == 49 {
			title = "末条技术发现"
			suggestion = "最终修复建议"
		}
		results = append(results, map[string]any{
			"title": title, "description": strings.Repeat("evidence ", 10),
			"risk_type": "security", "level": "high", "suggestion": suggestion,
		})
	}
	payload, err := json.Marshal(map[string]any{"score": 50, "results": results})
	require.NoError(t, err)
	completed := time.Date(2026, 8, 12, 18, 30, 0, 0, time.UTC)
	snapshot, err := BuildSnapshotAt("poppler-pdf", "alice", "Mcp-Scan", event(string(payload)), brand.Config{
		ProductName: "企业安全平台", PrimaryColor: "#1677FF", Watermark: "内部",
	}, completed, completed)
	require.NoError(t, err)
	snapshot.ID = "poppler-report"
	pdf, err := renderer.Render(context.Background(), snapshot)
	require.NoError(t, err)

	directory := t.TempDir()
	inputPath := filepath.Join(directory, "report.pdf")
	require.NoError(t, os.WriteFile(inputPath, pdf, 0o600))
	if qaDirectory := os.Getenv("AIG_REPORT_PDF_QA_DIR"); qaDirectory != "" {
		require.NoError(t, os.MkdirAll(qaDirectory, 0o755))
		inputPath = filepath.Join(qaDirectory, "report.pdf")
		require.NoError(t, os.WriteFile(inputPath, pdf, 0o600))
	}

	info, err := exec.Command("pdfinfo", inputPath).CombinedOutput()
	require.NoError(t, err, string(info))
	match := regexp.MustCompile(`(?m)^Pages:\s+(\d+)$`).FindStringSubmatch(string(info))
	require.Len(t, match, 2, string(info))
	pages, err := strconv.Atoi(match[1])
	require.NoError(t, err)
	assert.Greater(t, pages, 1)

	fonts, err := exec.Command("pdffonts", inputPath).CombinedOutput()
	require.NoError(t, err, string(fonts))
	assert.Regexp(t, regexp.MustCompile(`(?m)^.*report-cjk.*\byes\s+yes\s+yes\b`), string(fonts))

	textPath := filepath.Join(directory, "report.txt")
	textOutput, err := exec.Command("pdftotext", inputPath, textPath).CombinedOutput()
	require.NoError(t, err, string(textOutput))
	extracted, err := os.ReadFile(textPath)
	require.NoError(t, err)
	normalized := strings.Join(strings.Fields(string(extracted)), "")
	// Poppler interleaves the two independently positioned glyphs of the
	// rotated fixture watermark with nearby body text. Remove those known
	// watermark glyphs before asserting body-text continuity.
	normalized = strings.NewReplacer("内", "", "部", "").Replace(normalized)
	for _, expected := range []string{"企业安全平台", "安全报告", "生成时间", "末条技术发现", "最终修复建议"} {
		expected = strings.NewReplacer("内", "", "部", "").Replace(expected)
		assert.Contains(t, normalized, expected)
	}

	pagePrefix := filepath.Join(directory, "page")
	if qaDirectory := os.Getenv("AIG_REPORT_PDF_QA_DIR"); qaDirectory != "" {
		pagePrefix = filepath.Join(qaDirectory, "page")
	}
	renderOutput, err := exec.Command("pdftoppm", "-png", "-r", "96", inputPath, pagePrefix).CombinedOutput()
	require.NoError(t, err, string(renderOutput))
	firstPage, err := os.Stat(pagePrefix + "-1.png")
	require.NoError(t, err)
	assert.Greater(t, firstPage.Size(), int64(0))
}

func TestReportTextSplitsCJKAndLatinRunsWithoutDroppingDigitsOrIDs(t *testing.T) {
	runs := splitFontRuns("生成时间: 2026-08-12 risk-v2 task-pdf")
	require.Equal(t, []fontRun{
		{text: "生成时间", latin: false},
		{text: ": 2026-08-12 risk-v2 task-pdf", latin: true},
	}, runs)
}

func TestPDFLineWrappingKeepsShortLatinIdentifiersTogether(t *testing.T) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	require.NoError(t, pdf.AddTTFFontData(reportCJKFont, loadReportFont(t)))
	require.NoError(t, pdf.AddTTFFontData(reportLatinFont, goregular.TTF))

	lines, err := wrapMixedText(pdf,
		"技术发现 evidence evidence evidence remediation-14 technical-49 最终修复建议",
		14, 210, map[rune]float64{})
	require.NoError(t, err)
	for _, token := range []string{"remediation-14", "technical-49"} {
		assert.True(t, slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, token) }), "%s was split across lines: %#v", token, lines)
	}
}

func TestPDFPaginationMovesAWholeFindingBlockInsteadOfLeavingAnOrphanLine(t *testing.T) {
	assert.True(t, shouldStartLogicalLineOnNewPage(750, 4, 48, 790, 19))
	assert.False(t, shouldStartLogicalLineOnNewPage(48, 4, 48, 790, 19))
	assert.False(t, shouldStartLogicalLineOnNewPage(750, 50, 48, 790, 19), "a block larger than one page must still stream across pages")
}

func TestPDFRendererUsesOnlyImmutableRenderModelNotRawResult(t *testing.T) {
	renderer, err := NewPDFRenderer(loadReportFont(t))
	require.NoError(t, err)
	snapshot := reportPDFSnapshot(t, "", nil)
	first, err := renderer.Render(context.Background(), snapshot)
	require.NoError(t, err)
	snapshot.RawResult = []byte(`{"token":"changed-private-token","path":"C:/private"}`)
	second, err := renderer.Render(context.Background(), snapshot)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestPDFReportLinesUseOnlyWhitelistedInfrastructurePortScanDisplay(t *testing.T) {
	var fixed RenderModel
	require.NoError(t, json.Unmarshal([]byte(`{"task_type":"ai_infra_scan","port_scan_mode":"fixed_ai","port_spec":"11434,1337,7000-9000,18789"}`), &fixed))
	assert.Contains(t, reportLines(fixed), "端口扫描模式: 固定 AI 端口（11434,1337,7000-9000,18789）")

	var full RenderModel
	require.NoError(t, json.Unmarshal([]byte(`{"task_type":"ai_infra_scan","port_scan_mode":"full_tcp","port_spec":"1-65535"}`), &full))
	assert.Contains(t, reportLines(full), "端口扫描模式: 全量 TCP（1-65535）")

	var forged RenderModel
	require.NoError(t, json.Unmarshal([]byte(`{"task_type":"ai_infra_scan","port_scan_mode":"agent-supplied","port_spec":"sentinel-port-spec"}`), &forged))
	lines := strings.Join(reportLines(forged), "\n")
	assert.NotContains(t, lines, "agent-supplied")
	assert.NotContains(t, lines, "sentinel-port-spec")
}

func TestPDFRendererNeverConsumesNaturalLanguageOrStructuredCredentials(t *testing.T) {
	renderer, err := NewPDFRenderer(loadReportFont(t))
	require.NoError(t, err)
	snapshot, sentinels := credentialLeakSnapshot(t)
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	layout := strings.Join(reportLines(render), "\n")

	pdf, err := renderer.Render(context.Background(), snapshot)
	require.NoError(t, err)
	for _, sentinel := range sentinels {
		assert.NotContains(t, layout, sentinel)
		assert.NotContains(t, string(pdf), sentinel)
	}

	if _, err := exec.LookPath("pdftotext"); err == nil {
		directory := t.TempDir()
		inputPath := filepath.Join(directory, "credential-report.pdf")
		textPath := filepath.Join(directory, "credential-report.txt")
		require.NoError(t, os.WriteFile(inputPath, pdf, 0o600))
		output, err := exec.Command("pdftotext", inputPath, textPath).CombinedOutput()
		require.NoError(t, err, string(output))
		extracted, err := os.ReadFile(textPath)
		require.NoError(t, err)
		for _, sentinel := range sentinels {
			assert.NotContains(t, string(extracted), sentinel)
		}
	}
}

func TestPDFRendererRejectsMissingOrInvalidRenderModel(t *testing.T) {
	renderer, err := NewPDFRenderer(loadReportFont(t))
	require.NoError(t, err)
	for _, renderData := range [][]byte{nil, []byte(`{"render_version":"unknown"}`)} {
		snapshot := reportPDFSnapshot(t, "", nil)
		snapshot.RenderData = renderData
		_, err := renderer.Render(context.Background(), snapshot)
		require.Error(t, err)
	}
}

func TestNewEmbeddedPDFRendererUsesBundledProductionFont(t *testing.T) {
	renderer, err := NewEmbeddedPDFRenderer()
	require.NoError(t, err)
	require.NotNil(t, renderer)
}

func TestPDFRendererRejectsInvalidInputsWithoutLeakingSnapshotData(t *testing.T) {
	_, err := NewPDFRenderer([]byte("bad-font-private-path"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private")

	renderer, err := NewPDFRenderer(loadReportFont(t))
	require.NoError(t, err)
	for _, snapshot := range []*Snapshot{nil, reportPDFSnapshot(t, "image/svg+xml", []byte("<svg>secret</svg>")), reportPDFSnapshot(t, "image/png", []byte("not-a-logo-secret"))} {
		_, err = renderer.Render(context.Background(), snapshot)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret")
		assert.NotContains(t, err.Error(), "path")
	}
}

func loadReportFont(t *testing.T) []byte {
	t.Helper()
	font, err := reportAssets.ReadFile("assets/DroidSansFallbackFull.ttf")
	require.NoError(t, err)
	return font
}

func reportPDFSnapshot(t *testing.T, logoMIME string, logo []byte) *Snapshot {
	t.Helper()
	completed := time.Date(2026, 8, 12, 18, 30, 0, 0, time.UTC)
	snapshot, err := BuildSnapshotAt("task-pdf", "alice", "Mcp-Scan", event(`{"score":73,"results":[{"level":"high"},{"level":"high"},{"level":"medium"},{"level":"low"},{"level":"low"},{"level":"low"}]}`), brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF", Watermark: "内部", Logo: logo, LogoMIME: logoMIME}, completed, completed)
	require.NoError(t, err)
	snapshot.ID = "report-pdf"
	return snapshot
}

func onePixelPNG() []byte {
	value := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	value.Set(0, 0, color.NRGBA{R: 22, G: 119, B: 255, A: 255})
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, value)
	return encoded.Bytes()
}
