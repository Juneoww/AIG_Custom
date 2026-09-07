// 功能：从可信主服务下载任务内部归档，受限解压到专用临时目录。
// 输入：内部归档引用及 Agent 环境凭据；输出：本地源文件或固定安全错误。
package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const maxMCPArchiveBytes int64 = 64 << 20
const maxMCPArchiveFileBytes uint64 = 16 << 20

var errMCPArchive = errors.New("MCP repository archive unavailable")

func downloadMCPArchive(ctx context.Context, server, session, ref, dest string) error {
	base, err := mcpServerURL(server)
	id := strings.TrimPrefix(ref, "archive:")
	token := os.Getenv("AIG_AGENT_TOKEN")
	if err != nil || !strings.HasPrefix(ref, "archive:") || !mcpOpaqueID.MatchString(id) || !mcpOpaqueID.MatchString(session) || token == "" {
		return errMCPArchive
	}
	base.Path = "/api/internal/mcp-archives/" + session + "/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return errMCPArchive
	}
	req.Header.Set("X-Internal-Agent-Token", token)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errMCPArchive
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxMCPArchiveBytes {
		return errMCPArchive
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPArchiveBytes+1))
	if err != nil || int64(len(body)) > maxMCPArchiveBytes {
		return errMCPArchive
	}
	return extractMCPArchive(body, dest)
}

func extractMCPArchive(body []byte, dest string) error {
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || len(z.File) == 0 || len(z.File) > 10000 {
		return errMCPArchive
	}
	seen := map[string]bool{}
	var total uint64
	// 先验证所有路径和声明大小，失败时不创建部分目标文件。
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00") || name == ".." || strings.HasPrefix(name, "../") || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) || seen[strings.ToLower(name)] {
			return errMCPArchive
		}
		for _, part := range strings.Split(name, "/") {
			stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			if strings.TrimRight(part, ". ") != part || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9') {
				return errMCPArchive
			}
		}
		seen[strings.ToLower(name)] = true
		if f.UncompressedSize64 > maxMCPArchiveFileBytes {
			return errMCPArchive
		}
		total += f.UncompressedSize64
		if total > uint64(maxMCPArchiveBytes) {
			return errMCPArchive
		}
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return errMCPArchive
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return errMCPArchive
	}
	for _, f := range z.File {
		target := filepath.Join(root, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return errMCPArchive
		}
		if f.FileInfo().IsDir() {
			if os.MkdirAll(target, 0700) != nil {
				return errMCPArchive
			}
			continue
		}
		if os.MkdirAll(filepath.Dir(target), 0700) != nil {
			return errMCPArchive
		}
		input, err := f.Open()
		if err != nil {
			return errMCPArchive
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			input.Close()
			return errMCPArchive
		}
		n, copyErr := io.Copy(output, io.LimitReader(input, int64(maxMCPArchiveFileBytes)+1))
		closeErr := output.Close()
		input.Close()
		if copyErr != nil || closeErr != nil || n > int64(maxMCPArchiveFileBytes) || uint64(n) != f.UncompressedSize64 {
			return errMCPArchive
		}
	}
	return nil
}
