package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/require"
)

type mcpLockedAttachmentRepository struct {
	*MemoryRepository
	locked bool
}

func TestMCPAttachmentRetryRecoversOnlyUncommittedOwnedFiles(t *testing.T) {
	repository := NewMemoryRepository()
	service, err := NewAttachmentService(repository, AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 1024, MaxChunkBytes: 512, MCPOnly: true}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	ctx := context.Background()
	owner := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	file, err := service.BeginChunked(ctx, owner, "source.py", 3)
	require.NoError(t, err)
	chunk, err := service.storagePath(".chunks", file.ID, "chunk_00000000")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(chunk), 0700))
	require.NoError(t, os.WriteFile(chunk, []byte("abc"), 0600))
	complete := func(context.Context, int64) error { return nil }
	require.Error(t, service.UploadMCPChunk(ctx, owner, file.ID, 0, strings.NewReader("def"), complete))
	require.Error(t, service.UploadMCPChunk(ctx, owner, file.ID, 0, strings.NewReader("abc"), complete))
	unchanged, err := os.ReadFile(chunk)
	require.NoError(t, err)
	require.Equal(t, "abc", string(unchanged))
	// 模拟正常请求已提交分片计数，合并只恢复数据库未提交的临时归档。
	require.NoError(t, repository.AddAttachmentChunkBytes(ctx, file.ID, owner.UserID, 3, 3, time.Now()))
	stored, err := repository.GetAttachment(ctx, file.ID)
	require.NoError(t, err)
	final, err := service.storagePath(stored.StorageName)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(final, []byte("stale"), 0600))
	require.NoError(t, os.WriteFile(final+".merging", []byte("partial"), 0600))
	_, err = service.MergeMCP(ctx, owner, file.ID, 1, 3, complete)
	require.NoError(t, err)
	contents, err := os.ReadFile(final)
	require.NoError(t, err)
	require.Equal(t, "abc", string(contents))
}

func (repository *mcpLockedAttachmentRepository) WithinCreateKeyLock(ctx context.Context, owner, key string, apply func(context.Context) error) error {
	return repository.MemoryRepository.WithinCreateKeyLock(ctx, owner, key, func(locked context.Context) error {
		repository.locked = true
		defer func() { repository.locked = false }()
		return apply(locked)
	})
}
func (repository *mcpLockedAttachmentRepository) AddAttachmentChunkBytes(ctx context.Context, id, owner string, size, limit int64, now time.Time) error {
	if !repository.locked {
		return ErrInvalid
	}
	return repository.MemoryRepository.AddAttachmentChunkBytes(ctx, id, owner, size, limit, now)
}
func (repository *mcpLockedAttachmentRepository) MarkAttachmentReady(ctx context.Context, id, owner string, size int64, now time.Time) error {
	if !repository.locked {
		return ErrInvalid
	}
	return repository.MemoryRepository.MarkAttachmentReady(ctx, id, owner, size, now)
}
func TestMCPAttachmentMutationsShareResourceLock(t *testing.T) {
	repository := &mcpLockedAttachmentRepository{MemoryRepository: NewMemoryRepository()}
	service, err := NewAttachmentService(repository, AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 1024, MaxChunkBytes: 512, MCPOnly: true}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	ctx := context.Background()
	owner := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	file, err := service.BeginChunked(ctx, owner, "source.py", 3)
	require.NoError(t, err)
	complete := func(context.Context, int64) error { return nil }
	require.NoError(t, service.UploadMCPChunk(ctx, owner, file.ID, 0, strings.NewReader("abc"), complete))
	_, err = service.MergeMCP(ctx, owner, file.ID, 1, 3, complete)
	require.NoError(t, err)
	_, err = service.MergeMCP(ctx, owner, file.ID, 1, 3, complete)
	require.ErrorIs(t, err, ErrAttachmentNotReady)
	refs, err := service.ResolveReady(ctx, owner.UserID, []string{file.ID})
	require.NoError(t, err)
	require.Len(t, refs, 1)
}

func TestMCPAttachmentCannotCrossGenericBrowserBoundary(t *testing.T) {
	repository := NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	config := AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 1024, MaxChunkBytes: 512}
	generic, err := NewAttachmentService(repository, config, audits)
	require.NoError(t, err)
	config.MCPOnly = true
	dedicated, err := NewAttachmentService(repository, config, audits)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	file, err := dedicated.Upload(context.Background(), owner, "source.py", strings.NewReader("print('safe')"))
	require.NoError(t, err)
	_, _, _, err = generic.Open(context.Background(), owner, file.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = generic.ResolveReady(context.Background(), owner.UserID, []string{file.ID})
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, generic.Abort(context.Background(), owner, file.ID), ErrNotFound)
	ordinary, err := generic.Upload(context.Background(), owner, "plain.py", strings.NewReader("print('generic')"))
	require.NoError(t, err)
	_, err = dedicated.ResolveReady(context.Background(), owner.UserID, []string{ordinary.ID})
	require.ErrorIs(t, err, ErrNotFound)
	_, _, _, err = dedicated.Open(context.Background(), owner, file.ID)
	require.ErrorIs(t, err, ErrForbidden)
}
