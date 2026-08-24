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

package websocket

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Juneoww/AIG_Custom/common/fingerprints/parser"
	"github.com/gin-gonic/gin"
)

const maxKnowledgeRawBytes int64 = 1 << 20

var (
	errKnowledgeRawInvalid   = errors.New("invalid knowledge resource")
	errKnowledgeRawNotFound  = errors.New("knowledge resource not found")
	errKnowledgeRawTooLarge  = errors.New("knowledge resource too large")
	errKnowledgeRawAmbiguous = errors.New("knowledge resource is ambiguous")
)

type knowledgeRawResolver func(string) (string, error)

// HandleGetFingerprintRaw 返回指纹规则的原始 YAML，不经重新序列化。
func HandleGetFingerprintRaw(c *gin.Context) {
	newFingerprintKnowledgeRawHandler("data/fingerprints", "name")(c)
}

// HandleGetVulnerabilityRaw 在现有分类目录中按 CVE 定位原始 YAML。
func HandleGetVulnerabilityRaw(c *gin.Context) {
	newNestedKnowledgeRawHandler("data/vuln", "id", ".yaml")(c)
}

// HandleGetEvaluationRaw 返回评测集的原始 JSON，不改变字段顺序和空白。
func HandleGetEvaluationRaw(c *gin.Context) {
	newDirectKnowledgeRawHandler("data/eval", "name", ".json")(c)
}

func newDirectKnowledgeRawHandler(root, parameter, extension string) gin.HandlerFunc {
	return newKnowledgeRawHandler(root, parameter, func(name string) (string, error) {
		path, err := safeJoinPath(root, name+extension)
		if err != nil {
			return "", errKnowledgeRawInvalid
		}
		return path, nil
	})
}

func newFingerprintKnowledgeRawHandler(root, parameter string) gin.HandlerFunc {
	return newKnowledgeRawHandler(root, parameter, func(name string) (string, error) {
		return resolveFingerprintKnowledgeFile(root, name)
	})
}

func resolveFingerprintKnowledgeFile(root, name string) (string, error) {
	if err := validateKnowledgeRoot(root); err != nil {
		return "", err
	}
	matches := make([]string, 0, 1)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".yaml") {
			return nil
		}
		content, readErr := readKnowledgeRawFile(root, path)
		if readErr != nil {
			return readErr
		}
		fingerprint, parseErr := parser.InitFingerPrintFromData(content)
		if parseErr == nil && fingerprint != nil && fingerprint.Info.Name == name {
			matches = append(matches, path)
			if len(matches) > 1 {
				return errKnowledgeRawAmbiguous
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", errKnowledgeRawNotFound
	}
	return matches[0], nil
}

func newNestedKnowledgeRawHandler(root, parameter, extension string) gin.HandlerFunc {
	return newKnowledgeRawHandler(root, parameter, func(name string) (string, error) {
		if err := validateKnowledgeRoot(root); err != nil {
			return "", err
		}
		wanted := name + extension
		matches := make([]string, 0, 1)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() && strings.EqualFold(entry.Name(), wanted) {
				matches = append(matches, path)
				if len(matches) > 1 {
					return errKnowledgeRawAmbiguous
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return "", errKnowledgeRawNotFound
		}
		return matches[0], nil
	})
}

func newKnowledgeRawHandler(root, parameter string, resolve knowledgeRawResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param(parameter))
		if !isValidKnowledgeOpaqueName(name) {
			writeKnowledgeRawError(c, http.StatusBadRequest, "资源标识无效")
			return
		}

		path, err := resolve(name)
		if err != nil {
			writeKnowledgeRawResolutionError(c, err)
			return
		}
		content, err := readKnowledgeRawFile(root, path)
		if err != nil {
			writeKnowledgeRawResolutionError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  0,
			"message": "success",
			"data":    gin.H{"content": string(content)},
		})
	}
}

func isValidKnowledgeOpaqueName(name string) bool {
	if name == "" || len(name) > 256 || name == "." || name == ".." || !isValidName(name) {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return false
		}
	}
	return true
}

func validateKnowledgeRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errKnowledgeRawNotFound
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errKnowledgeRawInvalid
	}
	return nil
}

func readKnowledgeRawFile(root, path string) ([]byte, error) {
	if err := validateKnowledgeRoot(root); err != nil {
		return nil, err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, errKnowledgeRawInvalid
	}

	current := absoluteRoot
	parts := strings.Split(relative, string(os.PathSeparator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return nil, errKnowledgeRawNotFound
			}
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, errKnowledgeRawInvalid
		}
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errKnowledgeRawNotFound
		}
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(absolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errKnowledgeRawNotFound
		}
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		return nil, errKnowledgeRawInvalid
	}
	if openedInfo.Size() > maxKnowledgeRawBytes {
		return nil, errKnowledgeRawTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(file, maxKnowledgeRawBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxKnowledgeRawBytes {
		return nil, errKnowledgeRawTooLarge
	}
	return content, nil
}

func writeKnowledgeRawResolutionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errKnowledgeRawNotFound):
		writeKnowledgeRawError(c, http.StatusNotFound, "规则内容不存在")
	case errors.Is(err, errKnowledgeRawTooLarge):
		writeKnowledgeRawError(c, http.StatusRequestEntityTooLarge, "规则内容超过读取上限")
	case errors.Is(err, errKnowledgeRawInvalid), errors.Is(err, errKnowledgeRawAmbiguous):
		writeKnowledgeRawError(c, http.StatusBadRequest, "规则内容无法安全读取")
	default:
		writeKnowledgeRawError(c, http.StatusInternalServerError, "暂时无法读取规则内容")
	}
}

func writeKnowledgeRawError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"status": 1, "message": message})
}
