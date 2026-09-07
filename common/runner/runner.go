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

// Package runner 实现运行器
package runner

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Juneoww/AIG_Custom/common/fingerprints/parser"
	"github.com/Juneoww/AIG_Custom/common/fingerprints/preload"
	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"github.com/Juneoww/AIG_Custom/internal/options"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
	"github.com/Juneoww/AIG_Custom/pkg/vulstruct"

	"github.com/liushuochen/gotable"
	"github.com/logrusorgru/aurora"
	"github.com/projectdiscovery/fastdialer/fastdialer"
	"github.com/projectdiscovery/hmap/store/hybrid"
	"github.com/remeh/sizedwaitgroup"
	"go.uber.org/ratelimit"

	// automatic fd max increase if running as root
	_ "github.com/projectdiscovery/fdmax/autofdmax"
)

var (
	errTargetAuthScanRequest  = errors.New("基础设施认证目标请求失败")
	errTargetAuthAccessDenied = errors.New("基础设施目标认证访问被拒绝")
)

// Runner struct 保存运行指纹扫描所需的所有组件
type Runner struct {
	Options              *options.Options          // 配置选项
	hp                   *httpx.HTTPX              // HTTP 客户端
	hm                   *hybrid.HybridMap         // 混合存储
	rateLimiter          ratelimit.Limiter         // 速率限制器
	result               chan HttpResult           // 结果通道
	fpEngine             *preload.Runner           // 指纹引擎
	advEngine            *vulstruct.AdvisoryEngine // 漏洞建议引擎
	total                int                       // 总目标数
	done                 chan struct{}             // 用于优雅关闭的通道
	callback             func(interface{})
	runHostRequestFunc   func(string) error
	runDomainRequestFunc func(string) error
	targetAuthFailed     atomic.Bool
}

type Step01 struct {
	Text string
}

// New 初始化一个新的 Runner 实例
func New(options2 *options.Options) (*Runner, error) {
	runner := &Runner{
		Options: options2,
		total:   0,
		done:    make(chan struct{}), // 初始化done通道用于优雅关闭
	}

	targets, err := runner.parseTargets()
	if err != nil {
		return nil, err
	}

	// 依次初始化各个组件
	if err := runner.initStorage(); err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			runner.Close()
		}
	}()
	runner.storeTargets(targets)

	if err := runner.initComponents(); err != nil {
		return nil, err
	}

	if err := runner.initFingerprints(); err != nil {
		return nil, err
	}

	if err := runner.initVulnerabilityDB(); err != nil {
		return nil, err
	}

	success = true
	return runner, nil
}

// initFingerprints initializes the fingerprint detection engine
func (r *Runner) initFingerprints() error {
	options2 := r.Options
	fps := make([]parser.FingerPrint, 0)
	var err error
	if r.Options.LoadRemote {
		// 从远程加载
		fps, err = utils.LoadRemoteFingerPrints(options2.FPTemplates)
		if err != nil {
			return err
		}
	} else {
		// 初始化指纹
		if !utils.IsFileExists(options2.FPTemplates) {
			return fmt.Errorf("没有指定指纹模板文件:%s", options2.FPTemplates)
		}
		if utils.IsDir(options2.FPTemplates) {
			files, err := utils.ScanDir(options2.FPTemplates)
			if err != nil {
				return fmt.Errorf("无法扫描指纹模板目录:%s: %w", options2.FPTemplates, err)
			}
			for _, filename := range files {
				if !strings.HasSuffix(filename, ".yaml") {
					continue
				}
				data, err := os.ReadFile(filename)
				if err != nil {
					return fmt.Errorf("无法读取指纹模板文件:%s: %w", filename, err)
				}
				fp, err := parser.InitFingerPrintFromData(data)
				if err != nil {
					return fmt.Errorf("无法解析指纹模板文件:%s: %w", filename, err)
				}
				if options2.TargetAuth != nil && (strings.TrimSpace(fp.Info.Name) == "" || len(fp.Http) == 0) {
					return fmt.Errorf("无效指纹模板文件:%s: 缺少名称或 HTTP 规则", filename)
				}
				fps = append(fps, *fp)
			}
		} else {
			data, err := os.ReadFile(options2.FPTemplates)
			if err != nil {
				return fmt.Errorf("无法读取指纹模板文件:%s: %w", options2.FPTemplates, err)
			}
			fp, err := parser.InitFingerPrintFromData(data)
			if err != nil {
				return fmt.Errorf("无法解析指纹模板文件:%s: %w", options2.FPTemplates, err)
			}
			if options2.TargetAuth != nil && (strings.TrimSpace(fp.Info.Name) == "" || len(fp.Http) == 0) {
				return fmt.Errorf("无效指纹模板文件:%s: 缺少名称或 HTTP 规则", options2.FPTemplates)
			}
			fps = append(fps, *fp)
		}
	}
	if len(fps) == 0 {
		return fmt.Errorf("没有指定指纹模板")
	}
	r.fpEngine = preload.New(r.hp, fps)
	//text := fmt.Sprintf("加载指纹库,数量:%d", len(fps)+len(preload.CollectedFpReqs()))
	text := fmt.Sprintf("Loading fingerprints:%d", len(fps)+len(preload.CollectedFpReqs()))
	gologger.Infoln(text)
	if r.Options.Callback != nil {
		r.Options.Callback(Step01{Text: text})
	}

	r.result = make(chan HttpResult)
	return nil
}

// initStorage 初始化混合存储
func (r *Runner) initStorage() error {
	hm, err := hybrid.New(hybrid.DefaultDiskOptions)
	if err != nil {
		return fmt.Errorf("could not create temporary input file: %s", err)
	}
	r.hm = hm
	return nil
}

// collectTargetExpressions gathers raw target expressions from every configured source.
func (r *Runner) collectTargetExpressions() ([]string, error) {
	targets := append([]string(nil), r.Options.Target...)

	if r.Options.TargetFile != "" {
		file, err := os.Open(r.Options.TargetFile)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		targets, err = AppendTargetExpressionReader(targets, file)
		if err != nil {
			return nil, err
		}
	}

	if r.Options.LocalScan {
		op, err := utils.GetLocalOpenPorts()
		if err != nil {
			return nil, fmt.Errorf("get local open port: %w", err)
		}
		for _, p := range op {
			targets = append(targets, p.Address+":"+strconv.Itoa(p.Port))
		}
	}

	return targets, nil
}

// parseTargets parses all configured target sources as one batch.
func (r *Runner) parseTargets() ([]string, error) {
	if r.Options.TargetAuth != nil {
		if r.Options.TargetFile != "" || r.Options.LocalScan {
			return nil, fmt.Errorf("target authentication requires explicit URL targets")
		}
		if err := r.Options.TargetAuth.Validate(); err != nil {
			return nil, err
		}
		if err := httpx.ValidateTargetURLs(r.Options.TargetAuth.Origin, r.Options.Target, r.Options.TargetAuth.AllowInsecureHTTP); err != nil {
			return nil, err
		}
	}
	expressions, err := r.collectTargetExpressions()
	if err != nil {
		return nil, err
	}
	if r.Options.PreExpandedTargets {
		return deduplicatePreparedTargets(expressions), nil
	}
	return ParseTargets(expressions)
}

// deduplicatePreparedTargets retains trusted targets that have already passed
// ParseTargets while allowing Agent-discovered host:port entries beyond the
// raw expression expansion limit.
func deduplicatePreparedTargets(targets []string) []string {
	result := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		result = append(result, target)
	}
	return result
}

// storeTargets writes the parsed targets to storage and records their final count.
func (r *Runner) storeTargets(targets []string) {
	for _, target := range targets {
		r.hm.Set(target, nil)
	}
	r.total = len(targets)
	if r.total > 0 {
		gologger.Infof("加载目标数量:%d", r.total)
	}
}

// initComponents 初始化基础组件
// 包括速率限制器、HTTP客户端等
func (r *Runner) initComponents() error {
	// 初始化速率限制器
	r.rateLimiter = ratelimit.New(r.Options.RateLimit)
	r.result = make(chan HttpResult)

	// 初始化DNS解析器
	dialer, err := fastdialer.NewDialer(fastdialer.DefaultOptions)
	if err != nil {
		return fmt.Errorf("could not create resolver cache: %s", err)
	}

	// 配置HTTP客户端选项
	httpOptions := &httpx.HTTPOptions{
		Timeout:          time.Duration(r.Options.TimeOut) * time.Second,
		RetryMax:         1,
		FollowRedirects:  true,
		HTTPProxy:        r.Options.ProxyURL,
		Unsafe:           false,
		DefaultUserAgent: httpx.GetRandomUserAgent(),
		Dialer:           dialer,
		CustomHeaders:    r.Options.Headers,
		TargetAuth:       r.Options.TargetAuth,
	}

	// 创建HTTP客户端
	hp, err := httpx.NewHttpx(httpOptions)
	if err != nil {
		dialer.Close()
		return err
	}
	r.hp = hp
	return nil
}

// extractContent 处理 HTTP 响应并提取相关信息
func (r *Runner) extractContent(fullUrl string, resp *httpx.Response, respTime string) {
	builder := &strings.Builder{}
	builder.WriteString(fullUrl)

	builder.WriteString(" [")
	// 根据状态码设置不同颜色
	switch {
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		builder.WriteString(aurora.Green(strconv.Itoa(resp.StatusCode)).String()) // 2xx 绿色
	case resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest:
		builder.WriteString(aurora.Yellow(strconv.Itoa(resp.StatusCode)).String()) // 3xx 黄色
	case resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError:
		builder.WriteString(aurora.Red(strconv.Itoa(resp.StatusCode)).String()) // 4xx 红色
	case resp.StatusCode > http.StatusInternalServerError:
		builder.WriteString(aurora.Bold(aurora.Yellow(strconv.Itoa(resp.StatusCode))).String()) // 5xx 加粗黄色
	}
	builder.WriteString("] ")
	// 检测是否跳转,跳转则转过去，新建一个结果
	if r.Options.TargetAuth == nil && resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		newUrl := resp.GetHeader("Location")
		_ = r.runDomainRequest(newUrl)
	}

	title := resp.Title
	builder.WriteString(" [")
	builder.WriteString(title)
	builder.WriteString("] ")

	iconData, err := utils.GetFaviconBytes(r.hp, fullUrl, resp.Data)
	faviconHash := utils.FaviconHash(iconData)
	if err != nil {
		faviconHash = 0
	}
	// 内部指纹
	fpResults := r.fpEngine.RunFpReqs(fullUrl, 10, faviconHash)
	ads := make([]vulstruct.VersionVul, 0)
	isInternal := true
	if strings.Contains(fullUrl, "127.0.0.1") {
		isInternal = false
	}
	if strings.Contains(fullUrl, "localhost") {
		isInternal = false
	}
	if len(fpResults) > 0 {
		for _, item := range fpResults {
			builder.WriteString("[")
			builder.WriteString(item.Name)
			if item.Type != "" {
				builder.WriteString(":")
				builder.WriteString(item.Type)
			}
			if item.Version != "" {
				builder.WriteString(":")
				builder.WriteString(item.Version)
			}
			builder.WriteString("]")
			builder.WriteString(" ")

			advisories, err := r.advEngine.GetAdvisories(item.Name, item.Version, isInternal)
			if err != nil {
				gologger.Errorf("get advisory error: %s", err)
			} else {
				// 添加漏洞信息
				ads = append(ads, advisories...)
			}
			builder.WriteString(" ")
		}
	}

	result := HttpResult{
		URL:           fullUrl,
		Title:         title,
		ContentLength: resp.ContentLength,
		StatusCode:    resp.StatusCode,
		ResponseTime:  respTime,
		Fingers:       fpResults,
		s:             builder.String(),
		Advisories:    ads,
		Resp:          resp.DataStr,
	}
	r.result <- result
}

// runHostRequest 尝试使用 HTTP 和 HTTPS 连接到主机
func (r *Runner) runHostRequest(domain string) error {
	retried := false
	protocol := httpx.HTTP
retry:
	fullUrl := fmt.Sprintf("%s://%s", protocol, domain)
	timeStart := time.Now()
	headers := map[string]string{
		"tr": "a2802f09d2ddb7830a6f4b00910ab4f0",
	}
	resp, err := r.hp.Get(fullUrl, headers)
	if err != nil {
		if !retried {
			if protocol == httpx.HTTP {
				protocol = httpx.HTTPS
			} else {
				protocol = httpx.HTTP
			}
			retried = true
			goto retry
		}
		return err
	}
	r.extractContent(fullUrl, resp, time.Since(timeStart).String())
	return nil
}

// runDomainRequest makes a request to a specific URL and processes the response
func (r *Runner) runDomainRequest(fullUrl string) error {
	timeStart := time.Now()
	reqUrl := fullUrl
	headers := map[string]string{
		"tr": "a2802f09d2ddb7830a6f4b00910ab4f0",
	}
	resp, err := r.hp.Get(reqUrl, headers)
	if err != nil {
		if r.Options.TargetAuth != nil {
			return errTargetAuthScanRequest
		}
		return err
	}
	if r.Options.TargetAuth != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		return errTargetAuthAccessDenied
	}
	r.extractContent(reqUrl, resp, time.Since(timeStart).String())
	return nil
}

// Close cleans up resources used by the Runner
func (r *Runner) Close() {
	if r == nil {
		return
	}
	if r.hp != nil && r.hp.Options != nil && r.hp.Options.Dialer != nil {
		r.hp.Options.Dialer.Close()
	}
	if r.hm != nil {
		_ = r.hm.Close()
	}
}

func (r *Runner) callbackProcess(current, total int) {
	if r.Options.Callback != nil {
		r.Options.Callback(CallbackProcessInfo{
			Current: current,
			Total:   total,
		})
	}
}

// RunEnumeration 开始扫描所有目标
func (r *Runner) RunEnumeration() error {
	// 检查是否有输入目标
	if r.total == 0 {
		if r.Options.TargetAuth != nil {
			return errTargetAuthScanRequest
		}
		gologger.Fatalf("没有指定输入，输入 -h 查看帮助")
		return nil
	}
	r.targetAuthFailed.Store(false)
	r.callbackProcess(0, r.total)

	// 启动输出处理协程
	outputWg := sizedwaitgroup.New(1)
	outputWg.Add()
	go r.handleOutput(&outputWg)

	timeStart := time.Now()
	wg := sizedwaitgroup.New(r.Options.RateLimit)
	var numTarget uint64 = 0
	var requestError error
	var requestErrorOnce sync.Once
	recordRequestError := func(target string, err error) {
		if r.Options.TargetAuth != nil {
			r.targetAuthFailed.Store(true)
			if errors.Is(err, errTargetAuthAccessDenied) {
				err = errTargetAuthAccessDenied
			} else {
				err = errTargetAuthScanRequest
			}
			requestErrorOnce.Do(func() { requestError = err })
		}
		if r.Options.Callback != nil {
			r.Options.Callback(CallbackErrorInfo{Target: target, Error: err})
		}
	}

	r.hm.Scan(func(k, _ []byte) error {
		wg.Add()
		target := string(k)
		runHostRequest := r.runHostRequest
		runDomainRequest := r.runDomainRequest
		if r.runHostRequestFunc != nil {
			runHostRequest = r.runHostRequestFunc
		}
		if r.runDomainRequestFunc != nil {
			runDomainRequest = r.runDomainRequestFunc
		}
		if !isHTTPURL(target) {
			go func() {
				defer wg.Done()
				r.rateLimiter.Take()
				err := runHostRequest(target)
				if err != nil {
					recordRequestError(target, err)
				}
				atomic.AddUint64(&numTarget, 1)
				r.callbackProcess(int(atomic.LoadUint64(&numTarget)), r.total)
			}()
		} else {
			go func() {
				defer wg.Done()
				r.rateLimiter.Take()
				err := runDomainRequest(target)
				if err != nil {
					recordRequestError(target, err)
				}
				atomic.AddUint64(&numTarget, 1)
				r.callbackProcess(int(atomic.LoadUint64(&numTarget)), r.total)
			}()
		}
		return nil
	})
	wg.Wait()
	close(r.result)
	outputWg.Wait()
	if requestError != nil {
		return requestError
	}
	duration := time.Since(timeStart)
	gologger.Infof("扫描完成～耗时:%s", utils.Duration2String(duration))
	return nil
}

// handleOutput 处理扫描结果的输出
func (r *Runner) handleOutput(wg *sizedwaitgroup.SizedWaitGroup) {
	defer wg.Done()

	f, err := r.createOutputFile()
	if err != nil {
		gologger.Fatalf("创建输出文件失败: %v", err)
		return
	}
	if f != nil {
		defer f.Close()
	}
	var results []HttpResult
	for result := range r.result {
		results = append(results, result)
		r.writeResult(f, result)
	}
	// 主目标访问失败时保留已完成的证据，但不能生成成功汇总或安全评分。
	if r.Options.TargetAuth != nil && r.targetAuthFailed.Load() {
		return
	}
	// summary table
	if len(results) > 0 {
		table, err := gotable.Create("Target", "StatusCode", "Title", "FingerPrint")
		if err != nil {
			gologger.Errorf("create table error: %v", err)
			return
		}
		vulTable, err := gotable.Create("CVE", "Severity", "VulName", "Target", "Suggestions")
		if err != nil {
			gologger.Errorf("create table error:%v", err)
			return
		}
		var showVulTable bool = false
		for _, row := range results {
			data := make(map[string]string)
			var fpString string = ""
			for _, fp := range row.Fingers {
				fpString += fp.Name
				if fp.Type != "" {
					fpString += ":" + fp.Type
				}
				if fp.Version != "" {
					fpString += ":" + fp.Version
				}
			}
			data = map[string]string{
				"Target":      row.URL,
				"StatusCode":  fmt.Sprintf("%d", row.StatusCode),
				"Title":       row.Title,
				"FingerPrint": fpString,
			}
			table.AddRow(data)

			// write into vulTable
			for _, ad := range row.Advisories {
				showVulTable = true
				var adRow = []string{
					ad.Info.CVEName,
					ad.Info.Severity,
					ad.Info.Summary,
					row.URL,
					ad.Info.SecurityAdvise,
				}
				vulTable.AddRow(adRow)
			}
		}
		fmt.Println("Application Summary:")
		fmt.Println(table.String())
		if showVulTable {
			fmt.Println("Vulnerability Summary:")
			fmt.Println(vulTable.String())
		}
	}

	if r.Options.Callback != nil {
		advies := make([]vulstruct.Info, 0)
		for _, item := range results {
			for _, ad := range item.Advisories {
				advies = append(advies, ad.Info)
			}
		}
		score := r.CalcSecScore(advies)
		r.Options.Callback(score)
	}
}

// createOutputFile 创建输出文件
func (r *Runner) createOutputFile() (*os.File, error) {
	if r.Options.Output == "" {
		return nil, nil
	}
	return os.OpenFile(r.Options.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
}

// writeResult 写入扫描结果
func (r *Runner) writeResult(f *os.File, result HttpResult) {
	fmt.Println(result.s)
	if f != nil {
		_, _ = f.WriteString(result.s + "\n")
	}
	if r.Options.Callback != nil {
		vuls := make([]vulstruct.Info, 0)
		for _, item := range result.Advisories {
			vuls = append(vuls, item.Info)
		}
		var fpString string = ""
		for _, fp := range result.Fingers {
			fpString += fp.Name
			if fp.Type != "" {
				fpString += ":" + fp.Type
			}
			if fp.Version != "" {
				fpString += ":" + fp.Version
			}
		}
		if r.Options.Callback != nil {
			r.Options.Callback(CallbackScanResult{
				TargetURL:       result.URL,
				StatusCode:      result.StatusCode,
				Title:           result.Title,
				Fingerprint:     fpString,
				Vulnerabilities: vuls,
				Resp:            result.Resp,
			})
		}
	}
	if len(result.Advisories) > 0 {
		fmt.Println("\n存在漏洞:")
		for _, item := range result.Advisories {
			builder := strings.Builder{}
			builderFile := strings.Builder{}
			serverity := item.Info.Severity
			name := item.Info.CVEName
			if serverity == "HIGH" || serverity == "CRITICAL" {
				builder.WriteString(aurora.Red(fmt.Sprintf("%s [%s]", name, serverity)).String()) // 高危红色
			} else if serverity == "MEDIUM" {
				builder.WriteString(aurora.Yellow(fmt.Sprintf("%s [%s]", name, serverity)).String()) // 中危黄色
			} else {
				builder.WriteString(aurora.Bold(fmt.Sprintf("%s [%s]", name, serverity)).String()) // 低危加粗
			}
			builderFile.WriteString(fmt.Sprintf("%s [%s]\n", name, serverity))
			builder.WriteString(": " + item.Info.Summary + "\n" + item.Info.Details + "\n")
			builderFile.WriteString(": " + item.Info.Summary + "\n" + item.Info.Details + "\n")
			if len(item.Info.SecurityAdvise) > 0 {
				builder.WriteString("修复建议: " + item.Info.SecurityAdvise + "\n")
				builderFile.WriteString("修复建议: " + item.Info.SecurityAdvise + "\n")
			}
			fmt.Println(builder.String())
			_, _ = f.WriteString(builderFile.String() + "\n")
		}
	}
}

// GetFpAndVulList 获取指纹和漏洞列表
func (r *Runner) GetFpAndVulList() []FpInfos {
	fingerprints := make([]parser.FingerPrint, 0)
	for _, fp := range r.fpEngine.GetFps() {
		fp2 := fp
		fingerprints = append(fingerprints, fp2)
	}

	fps := make([]FpInfos, 0)
	for _, fp := range fingerprints {
		ads, err := r.advEngine.GetAdvisories(fp.Info.Name, "", false)
		if err != nil {
			gologger.WithError(err).Errorln("获取漏洞列表失败", fp)
			continue
		}
		fps = append(fps, FpInfos{
			FpName: fp.Info.Name,
			Vuls:   ads,
			Desc:   fp.Info.Desc,
		})
	}
	return fps
}

// ShowFpAndVulList displays the list of available fingerprints and vulnerabilities
// 显示指纹和漏洞列表
func (r *Runner) ShowFpAndVulList(vul bool) {
	data := r.GetFpAndVulList()
	if vul {
		gologger.Infoln("漏洞列表:")
		table, err := gotable.Create("组件名称", "组件简介", "漏洞数量")
		if err != nil {
			gologger.Errorf("create table error: %v", err)
			return
		}
		for _, item := range data {
			table.AddRow([]string{item.FpName, item.Desc, strconv.Itoa(len(item.Vuls))})
		}
		fmt.Println(table)
	}
}

// initVulnerabilityDB initializes the vulnerability advisory engine
func (r *Runner) initVulnerabilityDB() error {
	engine := vulstruct.NewAdvisoryEngine()
	var err error
	if r.Options.LoadRemote {
		// load from hostname
		err = engine.LoadFromHost(r.Options.AdvTemplates)
	} else {
		// load from directory
		vulDir := strings.TrimRight(r.Options.AdvTemplates, "/")
		if r.Options.Language == "en" {
			vulDir = vulDir + "_en"
		}
		if _, statErr := os.Stat(vulDir); statErr != nil {
			return fmt.Errorf("无法读取漏洞库:%s: %w", vulDir, statErr)
		}
		if r.Options.TargetAuth != nil {
			err = engine.LoadFromDirectoryStrict(vulDir)
		} else {
			err = engine.LoadFromDirectory(vulDir)
		}
		if err == nil && engine.GetCount() == 0 {
			return fmt.Errorf("漏洞库为空:%s", vulDir)
		}
	}
	if err != nil {
		return fmt.Errorf("无法初始化漏洞库: %w", err)
	}
	r.advEngine = engine
	// Load vulnerability version database
	text := fmt.Sprintf("Loading vulnerability database, count:%d", r.advEngine.GetCount())
	gologger.Infoln(text)
	if r.Options.Callback != nil {
		r.Options.Callback(Step01{Text: text})
	}
	return nil
}

// CalcSecScore 计算安全分数
func (r *Runner) CalcSecScore(advisories []vulstruct.Info) CallbackReportInfo {
	var total, high, middle, low int = 0, 0, 0, 0
	total = len(advisories)
	for _, item := range advisories {
		severity := strings.ToLower(strings.TrimSpace(item.Severity))
		if severity == "high" || severity == "critical" || severity == "高危" || severity == "严重" {
			high++
		} else if severity == "medium" || severity == "中危" {
			middle++
		} else {
			low++
		}
	}
	if total == 0 {
		return CallbackReportInfo{
			SecScore:   100,
			HighRisk:   0,
			MediumRisk: 0,
			LowRisk:    0,
		}
	}
	// 绝对扣分制：每个 critical/high -70，medium -30，low -10，扣到 0 为止
	deduction := high*70 + middle*30 + low*10
	safetyScore := 100 - deduction
	if safetyScore < 0 {
		safetyScore = 0
	}

	ret := CallbackReportInfo{
		SecScore:   safetyScore,
		HighRisk:   high,
		MediumRisk: middle,
		LowRisk:    low,
	}
	return ret
}
