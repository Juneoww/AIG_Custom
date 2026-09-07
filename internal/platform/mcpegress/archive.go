package mcpegress

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

const maxArchiveBytes int64 = 64 << 20
const maxArchiveFileBytes int64 = 16 << 20
const maxArchiveFiles = 10000
const archiveTTL = 15 * time.Minute

type archiveRecord struct {
	taskID, sessionID string
	data              []byte
	expires           time.Time
}

// RepositoryArchives 在平台内存中保存有界短时快照。引用只在受认证的 Agent
// 通道传递；重启后引用失效，重新分派必须重新验证来源和生成快照。
type RepositoryArchives struct {
	tasks       TaskReader
	attachments *tasks.AttachmentService
	policy      *mcpconnections.OutboundPolicy
	mu          sync.Mutex
	records     map[string]archiveRecord
	gate        chan struct{}
	now         func() time.Time
}

func NewRepositoryArchives(reader TaskReader, attachments *tasks.AttachmentService, policy *mcpconnections.OutboundPolicy) *RepositoryArchives {
	return &RepositoryArchives{tasks: reader, attachments: attachments, policy: policy, records: map[string]archiveRecord{}, gate: make(chan struct{}, 2), now: time.Now}
}

func (archives *RepositoryArchives) FetchRepository(ctx context.Context, input RepositoryFetchRequest) (string, error) {
	select {
	case archives.gate <- struct{}{}:
		defer func() { <-archives.gate }()
	case <-ctx.Done():
		return "", ErrRuntimeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	task, err := archives.tasks.Get(ctx, input.TaskID)
	if err != nil || !isMCPTask(task) || task.OwnerUserID != input.OwnerUserID || task.EngineSessionID == "" {
		return "", ErrRuntimeUnavailable
	}
	var data []byte
	if input.RepositoryURL != "" {
		data, err = fetchGitArchive(ctx, archives.policy, input.RepositoryURL)
	} else {
		data, err = archives.archiveAttachments(ctx, task)
	}
	if err != nil || len(data) == 0 || int64(len(data)) > maxArchiveBytes {
		return "", ErrRuntimeUnavailable
	}
	// 归档结束后重新确认分派会话，取消/重新分派不得复用旧引用。
	current, err := archives.tasks.Get(ctx, input.TaskID)
	if err != nil || !isMCPTask(current) || current.EngineSessionID != task.EngineSessionID {
		return "", ErrRuntimeUnavailable
	}
	token, err := randomCapability()
	if err != nil {
		return "", ErrRuntimeUnavailable
	}
	ref := archiveReferencePrefix + token
	archives.mu.Lock()
	defer archives.mu.Unlock()
	var stored int64
	for key, record := range archives.records {
		if !record.expires.After(archives.now()) || record.taskID == task.ID {
			delete(archives.records, key)
		} else {
			stored += int64(len(record.data))
		}
	}
	if len(archives.records) >= 128 || stored+int64(len(data)) > 2*maxArchiveBytes {
		return "", ErrRuntimeUnavailable
	}
	archives.records[ref] = archiveRecord{taskID: task.ID, sessionID: task.EngineSessionID, data: data, expires: archives.now().Add(archiveTTL)}
	return ref, nil
}

func (archives *RepositoryArchives) ReadArchive(ctx context.Context, sessionID, ref string) ([]byte, error) {
	if !validArchiveReference(ref) {
		return nil, ErrCapabilityDenied
	}
	archives.mu.Lock()
	record, ok := archives.records[ref]
	if ok && !record.expires.After(archives.now()) {
		delete(archives.records, ref)
		ok = false
	}
	archives.mu.Unlock()
	if !ok || record.sessionID != sessionID {
		return nil, ErrCapabilityDenied
	}
	task, err := archives.tasks.Get(ctx, record.taskID)
	if err != nil || !isMCPTask(task) || task.EngineSessionID != sessionID {
		return nil, ErrCapabilityDenied
	}
	return record.data, nil
}

func (archives *RepositoryArchives) archiveAttachments(ctx context.Context, task *tasks.Task) ([]byte, error) {
	if archives.attachments == nil || !archives.attachments.MCPOnly() {
		return nil, ErrRuntimeUnavailable
	}
	var ids []string
	if json.Unmarshal(task.AttachmentRefs, &ids) != nil || len(ids) == 0 || len(ids) > tasks.MaxTaskAttachmentCount {
		return nil, ErrRuntimeUnavailable
	}
	builder := newSourceArchive()
	for index, id := range ids {
		if ctx.Err() != nil {
			return nil, ErrRuntimeUnavailable
		}
		file, extension, err := archives.attachments.OpenMCPAttached(ctx, task.OwnerUserID, id)
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
		after, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return nil, ErrRuntimeUnavailable
		}
		if extension == ".zip" || extension == ".whl" {
			err = builder.addZIP(file, after.Size(), fmt.Sprintf("source_%d/", index+1))
		} else if extension == ".tgz" || extension == ".gz" {
			err = builder.addTGZ(file, fmt.Sprintf("source_%d/", index+1))
		} else {
			err = builder.add(fmt.Sprintf("source_%d%s", index+1, extension), file, after.Size())
		}
		file.Close()
		if err != nil {
			return nil, ErrRuntimeUnavailable
		}
	}
	return builder.finish()
}

func (builder *sourceArchive) addTGZ(reader io.Reader, prefix string) error {
	compressed, err := gzip.NewReader(io.LimitReader(reader, maxArchiveBytes+1))
	if err != nil {
		return ErrRuntimeUnavailable
	}
	defer compressed.Close()
	archive := tar.NewReader(io.LimitReader(compressed, maxArchiveBytes+(maxArchiveFiles*1024)))
	for count := 0; count < maxArchiveFiles; count++ {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return ErrRuntimeUnavailable
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safeArchiveName(name) {
			return ErrRuntimeUnavailable
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return ErrRuntimeUnavailable
		}
		if err := builder.add(prefix+name, archive, header.Size); err != nil {
			return err
		}
	}
	return ErrRuntimeUnavailable
}

type sourceArchive struct {
	output bytes.Buffer
	writer *zip.Writer
	names  map[string]bool
	bytes  int64
}

func newSourceArchive() *sourceArchive {
	result := &sourceArchive{names: map[string]bool{}}
	result.writer = zip.NewWriter(&result.output)
	return result
}
func safeArchiveName(name string) bool {
	if name == "" || len(name) > 1024 || strings.ContainsAny(name, "\\:\x00\r\n") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}
func (builder *sourceArchive) add(name string, reader io.Reader, size int64) error {
	key := strings.ToLower(name)
	if !safeArchiveName(name) || builder.names[key] || len(builder.names) >= maxArchiveFiles || size < 0 || size > maxArchiveFileBytes || builder.bytes+size > maxArchiveBytes {
		return ErrRuntimeUnavailable
	}
	builder.names[key] = true
	w, err := builder.writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return ErrRuntimeUnavailable
	}
	written, err := io.Copy(w, io.LimitReader(reader, size+1))
	if err != nil || written != size {
		return ErrRuntimeUnavailable
	}
	builder.bytes += written
	return nil
}
func (builder *sourceArchive) addZIP(reader io.ReaderAt, size int64, prefix string) error {
	if err := preflightZIPDirectory(reader, size); err != nil {
		return ErrRuntimeUnavailable
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil || len(archive.File) > maxArchiveFiles {
		return ErrRuntimeUnavailable
	}
	for _, file := range archive.File {
		name := strings.TrimSuffix(file.Name, "/")
		if !safeArchiveName(name) || file.Mode()&os.ModeSymlink != 0 || (!file.Mode().IsRegular() && !file.FileInfo().IsDir()) {
			return ErrRuntimeUnavailable
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if file.UncompressedSize64 > uint64(maxArchiveFileBytes) {
			return ErrRuntimeUnavailable
		}
		r, err := file.Open()
		if err != nil {
			return ErrRuntimeUnavailable
		}
		err = builder.add(prefix+name, r, int64(file.UncompressedSize64))
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func (builder *sourceArchive) finish() ([]byte, error) {
	if len(builder.names) == 0 || builder.writer.Close() != nil || int64(builder.output.Len()) > maxArchiveBytes {
		return nil, ErrRuntimeUnavailable
	}
	return builder.output.Bytes(), nil
}
