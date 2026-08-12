package reports

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/goregular"
)

//go:embed assets/DroidSansFallbackFull.ttf
var reportAssets embed.FS

const (
	// PDF readers recognize an embedded subset only when BaseFont starts with
	// six uppercase letters and '+'. gopdf subsets the glyph data but preserves
	// the supplied family name, so keep stable compliant prefixes here.
	reportCJKFont   = "AIGCJK+report-cjk"
	reportLatinFont = "AIGLAT+report-latin"
)

type pdfRenderer struct{ cjkFont []byte }

type fontRun struct {
	text  string
	latin bool
}

func NewPDFRenderer(fontBytes []byte) (PDFRenderer, error) {
	if len(fontBytes) == 0 || !validFont(fontBytes) {
		return nil, errors.New("报告 PDF 字体无效")
	}
	return &pdfRenderer{cjkFont: append([]byte(nil), fontBytes...)}, nil
}

// NewEmbeddedPDFRenderer constructs the production renderer from the bundled,
// reviewed font asset so server composition never depends on a host font path.
func NewEmbeddedPDFRenderer() (PDFRenderer, error) {
	font, err := reportAssets.ReadFile("assets/DroidSansFallbackFull.ttf")
	if err != nil {
		return nil, errors.New("报告 PDF 字体无效")
	}
	return NewPDFRenderer(font)
}

func validFont(font []byte) bool {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	return pdf.AddTTFFontData(reportCJKFont, font) == nil
}

func (renderer *pdfRenderer) Render(_ context.Context, snapshot *Snapshot) ([]byte, error) {
	if renderer == nil || len(renderer.cjkFont) == 0 || !validPDFSnapshot(snapshot) {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	var render RenderModel
	if json.Unmarshal(snapshot.RenderData, &render) != nil || render.RenderVersion != "report-render-v1" && render.RenderVersion != "report-render-v2" ||
		render.TaskID != snapshot.TaskID || render.TaskType != snapshot.TaskType || !render.CompletedAt.Equal(snapshot.CompletedAt) {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	if len(snapshot.Brand.Logo) > 0 && snapshot.Brand.LogoMIME == "image/svg+xml" {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.SetNoCompression()
	if pdf.AddTTFFontData(reportCJKFont, renderer.cjkFont) != nil || pdf.AddTTFFontData(reportLatinFont, goregular.TTF) != nil {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	pdf.AddPage()
	if err := renderWatermark(pdf, render.Watermark); err != nil {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	if err := renderLogo(pdf, snapshot.Brand.LogoMIME, snapshot.Brand.Logo); err != nil {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	if err := renderReportText(pdf, render); err != nil {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	var output bytes.Buffer
	if err := pdf.Write(&output); err != nil {
		return nil, errors.New("报告 PDF 渲染失败")
	}
	return output.Bytes(), nil
}

func validPDFSnapshot(snapshot *Snapshot) bool {
	return snapshot != nil && strings.TrimSpace(snapshot.ID) != "" && strings.TrimSpace(snapshot.TaskID) != "" &&
		strings.TrimSpace(snapshot.TaskType) != "" && !snapshot.CompletedAt.IsZero() && snapshot.Risk.Score >= 0 && snapshot.Risk.Score <= 100
}

func renderReportText(pdf *gopdf.GoPdf, render RenderModel) error {
	const (
		left       = 48.0
		top        = 48.0
		bottom     = 790.0
		lineHeight = 19.0
		fontSize   = 14.0
		lineWidth  = 430.0
	)
	y := top
	widthCache := map[rune]float64{}
	for _, logicalLine := range reportLines(render) {
		wrapped, err := wrapMixedText(pdf, logicalLine, fontSize, lineWidth, widthCache)
		if err != nil {
			return err
		}
		if shouldStartLogicalLineOnNewPage(y, len(wrapped), top, bottom, lineHeight) {
			pdf.AddPage()
			if err := renderWatermark(pdf, render.Watermark); err != nil {
				return err
			}
			y = top
		}
		for _, line := range wrapped {
			if y+lineHeight > bottom {
				pdf.AddPage()
				if err := renderWatermark(pdf, render.Watermark); err != nil {
					return err
				}
				y = top
			}
			if err := renderMixedText(pdf, line, left, y, fontSize); err != nil {
				return err
			}
			y += lineHeight
		}
	}
	return nil
}

func shouldStartLogicalLineOnNewPage(y float64, wrappedLines int, top, bottom, lineHeight float64) bool {
	if wrappedLines <= 1 {
		return false
	}
	blockHeight := float64(wrappedLines) * lineHeight
	return blockHeight <= bottom-top && y+blockHeight > bottom
}

func wrapMixedText(pdf *gopdf.GoPdf, text string, size, maxWidth float64, cache map[rune]float64) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return []string{""}, nil
	}
	lines := make([]string, 0, 2)
	current := make([]rune, 0, len(text))
	currentWidth := 0.0
	for _, unit := range mixedWrapUnits(text) {
		unitWidth := 0.0
		for _, value := range unit {
			width, err := reportRuneWidth(pdf, value, size, cache)
			if err != nil {
				return nil, err
			}
			unitWidth += width
		}
		if currentWidth+unitWidth > maxWidth && len(current) > 0 {
			lines = append(lines, strings.TrimSpace(string(current)))
			current = current[:0]
			currentWidth = 0
		}
		if strings.TrimSpace(unit) == "" && len(current) == 0 {
			continue
		}
		if unitWidth <= maxWidth {
			current = append(current, []rune(unit)...)
			currentWidth += unitWidth
			continue
		}
		// A single unbroken Latin token may itself exceed the available line.
		// Only that exceptional token is split by rune to prevent clipping.
		for _, value := range unit {
			width, err := reportRuneWidth(pdf, value, size, cache)
			if err != nil {
				return nil, err
			}
			if currentWidth+width > maxWidth && len(current) > 0 {
				lines = append(lines, strings.TrimSpace(string(current)))
				current = current[:0]
				currentWidth = 0
			}
			current = append(current, value)
			currentWidth += width
		}
	}
	if len(current) > 0 {
		lines = append(lines, strings.TrimSpace(string(current)))
	}
	if len(lines) == 0 {
		return []string{""}, nil
	}
	return lines, nil
}

func mixedWrapUnits(text string) []string {
	units := make([]string, 0, len(text))
	latin := make([]rune, 0, 16)
	flushLatin := func() {
		if len(latin) > 0 {
			units = append(units, string(latin))
			latin = latin[:0]
		}
	}
	for _, value := range text {
		switch {
		case unicode.IsSpace(value):
			flushLatin()
			units = append(units, string(value))
		case value <= unicode.MaxLatin1 || unicode.In(value, unicode.Latin):
			latin = append(latin, value)
		default:
			flushLatin()
			units = append(units, string(value))
		}
	}
	flushLatin()
	return units
}

func reportRuneWidth(pdf *gopdf.GoPdf, value rune, size float64, cache map[rune]float64) (float64, error) {
	if width, exists := cache[value]; exists {
		return width, nil
	}
	font := reportCJKFont
	if value <= unicode.MaxLatin1 || unicode.In(value, unicode.Latin) {
		font = reportLatinFont
	}
	if err := pdf.SetFont(font, "", size); err != nil {
		return 0, err
	}
	width, err := pdf.MeasureTextWidth(string(value))
	if err != nil {
		return 0, err
	}
	cache[value] = width
	return width, nil
}

func reportLines(render RenderModel) []string {
	trendCompleted := 0
	for _, point := range render.RiskTrend {
		trendCompleted += point.Completed
	}
	lines := []string{
		render.ProductName,
		"安全报告",
		"生成时间: " + render.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		"风险评分: " + itoa(render.Risk.Score),
		"评分说明: " + render.ScoreExplanation,
		"风险趋势: 最近30个UTC自然日完成 " + itoa(trendCompleted) + " 个任务",
		"风险分布 高: " + itoa(render.RiskDistribution.High) + " 中: " + itoa(render.RiskDistribution.Medium) + " 低: " + itoa(render.RiskDistribution.Low),
		"Top 风险:",
	}
	for _, item := range render.TopRisks {
		lines = append(lines, "- "+item.Severity+" "+itoa(item.Count)+" 项；"+item.Impact+"；"+item.Remediation)
	}
	lines = append(lines, "优先处置建议:")
	for _, recommendation := range render.Recommendations {
		lines = append(lines, "- "+recommendation)
	}
	lines = append(lines,
		"扫描覆盖范围: "+render.Coverage,
		"任务 ID: "+render.TaskID,
		"任务类型: "+render.TaskType,
		"结论: "+render.Conclusion,
		"技术发现:",
	)
	if len(render.TechnicalFindings) == 0 {
		lines = append(lines, "- 当前标准化映射仅提供聚合风险；技术明细保留在受控引擎结果中。")
	} else {
		for _, finding := range render.TechnicalFindings {
			lines = append(lines, "- "+finding.Title+"；证据: "+finding.Evidence+"；影响: "+finding.Impact+"；修复: "+finding.Remediation)
		}
	}
	return lines
}

func renderWatermark(pdf *gopdf.GoPdf, watermark string) error {
	if strings.TrimSpace(watermark) == "" {
		return nil
	}
	pdf.SetTextColor(210, 210, 210)
	pdf.Rotate(35, 300, 420)
	err := renderMixedText(pdf, truncateFindingText(watermark, 64, 192), 180, 420, 14)
	pdf.RotateReset()
	pdf.SetTextColor(0, 0, 0)
	return err
}

func renderMixedText(pdf *gopdf.GoPdf, text string, x, y, size float64) error {
	for _, run := range splitFontRuns(text) {
		font := reportCJKFont
		if run.latin {
			font = reportLatinFont
		}
		if err := pdf.SetFont(font, "", size); err != nil {
			return err
		}
		pdf.SetX(x)
		pdf.SetY(y)
		if err := pdf.Text(run.text); err != nil {
			return err
		}
		width, err := pdf.MeasureTextWidth(run.text)
		if err != nil {
			return err
		}
		x += width
	}
	return nil
}

func splitFontRuns(text string) []fontRun {
	var runs []fontRun
	for _, value := range text {
		latin := value <= unicode.MaxLatin1 || unicode.In(value, unicode.Latin)
		if len(runs) == 0 || runs[len(runs)-1].latin != latin {
			runs = append(runs, fontRun{latin: latin})
		}
		runs[len(runs)-1].text += string(value)
	}
	return runs
}

func renderLogo(pdf *gopdf.GoPdf, mime string, logo []byte) error {
	if len(logo) == 0 {
		return nil
	}
	if mime != "image/png" && mime != "image/jpeg" {
		return errors.New("unsupported logo")
	}
	decoded, format, err := image.Decode(bytes.NewReader(logo))
	if err != nil || (mime == "image/png" && format != "png") || (mime == "image/jpeg" && format != "jpeg") {
		return errors.New("invalid logo")
	}
	return pdf.ImageFrom(decoded, 470, 45, &gopdf.Rect{W: 54, H: 54})
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}
