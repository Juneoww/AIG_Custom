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

package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Juneoww/AIG_Custom/common/fingerprints/parser"
	"github.com/Juneoww/AIG_Custom/internal/gologger"
)

var ErrDownloadTooLarge = errors.New("download exceeds size limit")

// DownloadFile 下载文件
// path 参数必须由调用方在调用前完成路径安全校验（防止路径穿越），
// 本函数仅负责 HTTP 下载写入，不做路径验证。
func DownloadFile(server, sessionId, uri, path string) error {
	return downloadFile(server, sessionId, uri, path, nil)
}

// DownloadFileBounded downloads an attachment without writing more than maxBytes to disk.
func DownloadFileBounded(server, sessionId, uri, path string, maxBytes int64) error {
	if maxBytes < 0 || maxBytes >= math.MaxInt64 {
		return fmt.Errorf("download size limit is not representable")
	}
	return downloadFile(server, sessionId, uri, path, &maxBytes)
}

func downloadFile(server, sessionId, uri, path string, maxBytes *int64) error {
	// Validate that path is not empty and does not contain path traversal sequences.
	// Callers are responsible for ensuring path is within an expected directory.
	if path == "" || strings.Contains(path, "..") {
		return fmt.Errorf("非法文件路径")
	}
	agentToken := strings.TrimSpace(os.Getenv("AIG_AGENT_TOKEN"))
	if agentToken == "" {
		return fmt.Errorf("AIG_AGENT_TOKEN is required")
	}
	// 创建 HTTP 客户端
	client := &http.Client{}

	data := map[string]string{
		"fileUrl": uri,
	}
	jsonData, err := json.Marshal(data)
	// 创建请求并添加 header
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/api/v1/app/tasks/%s/downloadFile", server, sessionId), io.NopCloser(bytes.NewBuffer(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Agent-Token", agentToken)

	// 发送 POST 请求
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		if maxBytes != nil {
			return fmt.Errorf("下载失败，HTTP 状态码：%d", resp.StatusCode)
		}
		dd, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("下载失败，HTTP 状态码：%d content:%s", resp.StatusCode, string(dd))
	}

	if maxBytes != nil {
		contents, readErr := io.ReadAll(io.LimitReader(resp.Body, *maxBytes+1))
		if readErr != nil {
			return readErr
		}
		if int64(len(contents)) > *maxBytes {
			return ErrDownloadTooLarge
		}
		return os.WriteFile(path, contents, 0600)
	}

	// 创建文件
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	// 将响应体复制到文件
	_, err = io.Copy(file, resp.Body)
	return err
}

// UploadFileResponse 上传文件响应结构
type UploadFileResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Data    struct {
		FileUrl  string `json:"fileUrl"`
		Filename string `json:"filename"`
	} `json:"data"`
}

// UploadFile 上传文件到服务器
func UploadFile(server, sessionID, filePath string) (*UploadFileResponse, error) {
	agentToken := strings.TrimSpace(os.Getenv("AIG_AGENT_TOKEN"))
	if agentToken == "" {
		return nil, fmt.Errorf("AIG_AGENT_TOKEN is required")
	}
	if strings.TrimSpace(sessionID) == "" || strings.ContainsAny(sessionID, `/\\`) {
		return nil, fmt.Errorf("无效的任务会话")
	}
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("无法打开文件")
	}
	defer file.Close()

	reader, writerPipe := io.Pipe()
	multipartWriter := multipart.NewWriter(writerPipe)
	copyResult := make(chan error, 1)
	go func() {
		part, createErr := multipartWriter.CreateFormFile("file", filepath.Base(filePath))
		if createErr == nil {
			_, createErr = io.Copy(part, file)
		}
		if closeErr := multipartWriter.Close(); createErr == nil {
			createErr = closeErr
		}
		_ = writerPipe.CloseWithError(createErr)
		copyResult <- createErr
	}()

	// 创建 HTTP 请求
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/api/v1/app/tasks/%s/uploadFile", server, sessionID), reader)
	if err != nil {
		_ = reader.Close()
		<-copyResult
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 设置 Content-Type
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("X-Internal-Agent-Token", agentToken)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		_ = reader.Close()
		<-copyResult
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()
	if err := <-copyResult; err != nil {
		return nil, fmt.Errorf("上传流失败")
	}

	// 读取响应体
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上传失败，HTTP 状态码：%d", resp.StatusCode)
	}

	// 解析响应 JSON
	var uploadResp UploadFileResponse
	err = json.Unmarshal(respBody, &uploadResp)
	if err != nil {
		return nil, fmt.Errorf("解析响应 JSON 失败: %v", err)
	}

	return &uploadResp, nil
}

func GetEvaluationsDetail(server, name string) ([]byte, error) {
	path := "/api/v1/knowledge/evaluations/" + name
	// 创建 HTTP 请求
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s%s", server, path), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("X-APIKey", "zhuque")

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上传失败，HTTP 状态码：%d content:%s", resp.StatusCode, string(respBody))
	}

	var msg struct {
		Data json.RawMessage `json:"data"`
	}
	err = json.Unmarshal(respBody, &msg)
	if err != nil {
		return nil, fmt.Errorf("解析响应 JSON 失败: %v", err)
	}
	return msg.Data, nil
}

func LoadRemoteFingerPrints(hostname string) ([]parser.FingerPrint, error) {
	type msg struct {
		Data struct {
			FingerPrints []json.RawMessage `json:"items"`
			Total        int               `json:"total"`
		} `json:"data"`
		Message string `json:"message"`
	}
	// 创建请求并添加 header
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/api/v1/knowledge/fingerprints?page=1&size=9999", hostname), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-APIKey", "zhuque")

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status code: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var m msg
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	fps := make([]parser.FingerPrint, 0)
	for _, raw := range m.Data.FingerPrints {
		fp, err := parser.InitFingerPrintFromData(raw)
		if err != nil {
			gologger.WithError(err).Fatalf("无法解析指纹模板:%s", string(raw))
			continue
		}
		fps = append(fps, *fp)
	}
	return fps, nil
}

func LoadRemoteVulStruct(api string) ([]json.RawMessage, error) {
	type msg struct {
		Data struct {
			Vuls  []json.RawMessage `json:"items"`
			Total int               `json:"total"`
		} `json:"data"`
		Message string `json:"message"`
	}
	// 创建请求并添加 header
	req, err := http.NewRequest("GET", api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-APIKey", "zhuque")

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status code: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var m msg
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m.Data.Vuls, nil
}
