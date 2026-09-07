package mcpegress

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// fetchGitArchive 直接使用独立 go-git 会话，不修改进程级协议表。
// 仅取默认 HEAD 的浅快照；禁认证、重定向、代理、子模块和工作树执行。
func fetchGitArchive(ctx context.Context, policy *mcpconnections.OutboundPolicy, rawURL string) ([]byte, error) {
	if policy == nil || policy.RequireControlledDialer() != nil || policy.ValidateGitURL(ctx, rawURL) != nil {
		return nil, ErrRuntimeUnavailable
	}
	endpoint, err := transport.NewEndpoint(rawURL)
	if err != nil || endpoint.Protocol != "https" || endpoint.User != "" || endpoint.Password != "" {
		return nil, ErrRuntimeUnavailable
	}
	httpTransport := &http.Transport{Proxy: nil, DialContext: policy.DialGitContext, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	defer httpTransport.CloseIdleConnections()
	client := &http.Client{Transport: &gitResponseLimit{base: httpTransport}, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRuntimeUnavailable }}
	session, err := githttp.NewClient(client).NewUploadPackSession(endpoint, nil)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	defer session.Close()
	return gitArchiveFromSession(ctx, session)
}

func gitArchiveFromSession(ctx context.Context, session transport.UploadPackSession) ([]byte, error) {
	advertised, err := session.AdvertisedReferencesContext(ctx)
	if err != nil || advertised.Head == nil || advertised.Head.IsZero() || !advertised.Capabilities.Supports(capability.Shallow) {
		return nil, ErrRuntimeUnavailable
	}
	request := packp.NewUploadPackRequestFromCapabilities(advertised.Capabilities)
	request.Wants = []plumbing.Hash{*advertised.Head}
	request.Depth = packp.DepthCommits(1)
	_ = request.Capabilities.Set(capability.Shallow)
	response, err := session.UploadPack(ctx, request)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	defer response.Close()
	raw, err := io.ReadAll(io.LimitReader(response, maxArchiveBytes+1))
	if err != nil || preflightGitPack(ctx, raw) != nil {
		return nil, ErrRuntimeUnavailable
	}
	store := newBoundedGitStorage()
	if err := packfile.UpdateObjectStorage(store, bytes.NewReader(raw)); err != nil {
		return nil, ErrRuntimeUnavailable
	}
	commit, err := object.GetCommit(store, *advertised.Head)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()
	builder := newSourceArchive()
	for {
		if ctx.Err() != nil {
			return nil, ErrRuntimeUnavailable
		}
		name, entry, err := walker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
		if !safeArchiveName(name) || strings.EqualFold(name, ".gitmodules") {
			return nil, ErrRuntimeUnavailable
		}
		switch entry.Mode {
		case filemode.Dir:
			continue
		case filemode.Regular, filemode.Executable:
		default:
			return nil, ErrRuntimeUnavailable
		}
		blob, err := object.GetBlob(store, entry.Hash)
		if err != nil || blob.Size > maxArchiveFileBytes {
			return nil, ErrRuntimeUnavailable
		}
		r, err := blob.Reader()
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
		err = builder.add(name, r, blob.Size)
		r.Close()
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
	}
	return builder.finish()
}

// Git 的广告与 pack 都有传输上限，非成功响应不进入 go-git 的错误文本。
type gitResponseLimit struct{ base http.RoundTripper }

func (transport *gitResponseLimit) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.User != nil || request.Header.Get("Authorization") != "" {
		return nil, ErrRuntimeUnavailable
	}
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	if response.StatusCode != http.StatusOK || response.ContentLength > maxArchiveBytes {
		response.Body.Close()
		return nil, ErrRuntimeUnavailable
	}
	limit := maxArchiveBytes
	if request.Method == http.MethodGet {
		limit = 4 << 20
	}
	response.Body = &limitedGitBody{Reader: io.LimitReader(response.Body, limit+1), Closer: response.Body}
	return response, nil
}

type limitedGitBody struct {
	io.Reader
	io.Closer
}

// 隐藏 PackfileWriter 快速路径；这里仅作存储层防御，解压安全依赖前置预检。
type boundedGitStorage struct {
	storer.Storer
	budget  int64
	objects int
}

func newBoundedGitStorage() *boundedGitStorage {
	return &boundedGitStorage{Storer: memory.NewStorage(), budget: 2 * maxArchiveBytes}
}
func (storage *boundedGitStorage) NewEncodedObject() plumbing.EncodedObject {
	return &boundedGitObject{MemoryObject: &plumbing.MemoryObject{}, storage: storage}
}
func (storage *boundedGitStorage) SetEncodedObject(object plumbing.EncodedObject) (plumbing.Hash, error) {
	storage.objects++
	if storage.objects > maxArchiveFiles*3 || object.Size() > maxArchiveFileBytes {
		return plumbing.ZeroHash, ErrRuntimeUnavailable
	}
	return storage.Storer.SetEncodedObject(object)
}

type boundedGitObject struct {
	*plumbing.MemoryObject
	storage *boundedGitStorage
}

func (object *boundedGitObject) Writer() (io.WriteCloser, error) {
	writer, err := object.MemoryObject.Writer()
	if err != nil {
		return nil, err
	}
	return &boundedGitWriter{WriteCloser: writer, storage: object.storage}, nil
}

type boundedGitWriter struct {
	io.WriteCloser
	storage *boundedGitStorage
	written int64
}

func (writer *boundedGitWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.storage.budget || writer.written+int64(len(data)) > maxArchiveFileBytes {
		return 0, ErrRuntimeUnavailable
	}
	written, err := writer.WriteCloser.Write(data)
	writer.storage.budget -= int64(written)
	writer.written += int64(written)
	return written, err
}
