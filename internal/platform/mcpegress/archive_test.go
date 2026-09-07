package mcpegress

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/require"
)

func TestMCPArchiveUsesOpaqueSessionBoundReference(t *testing.T) {
	ctx := context.Background()
	repository := tasks.NewMemoryRepository()
	attachments, err := tasks.NewAttachmentService(repository, tasks.AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 1024, MaxChunkBytes: 512, MCPOnly: true}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "owner", Role: identity.RoleUser}
	file, err := attachments.Upload(ctx, owner, "private-name.py", strings.NewReader("print('hello')"))
	require.NoError(t, err)
	require.NoError(t, repository.BindReadyAttachments(ctx, owner.UserID, []string{file.ID}, time.Now()))
	ids, _ := json.Marshal([]string{file.ID})
	_, _, err = repository.CreateOrGet(ctx, &tasks.Task{ID: "task", OwnerUserID: owner.UserID, IdempotencyKey: "archive", TaskType: "mcp_scan", Status: tasks.StatusRunning, EngineSessionID: "session", AttachmentRefs: ids})
	require.NoError(t, err)
	archives := NewRepositoryArchives(repository, attachments, nil)
	keyring := testEgressKeyring(t)
	binding := &mcpconnections.TaskBinding{ID: "binding", TaskID: "task", SourceKind: "repository"}
	require.NoError(t, keyring.SealRepositorySource(binding, mcpconnections.BindingEncryptionContext{OwnerUserID: owner.UserID, Scope: mcpconnections.ScopePrivate, Version: 1}, mcpconnections.RepositorySourceSnapshot{}))
	issuer := NewService(ServiceDependencies{Tasks: repository, Bindings: &memoryBindingReader{bindings: map[string]*mcpconnections.TaskBinding{"task": binding}}, Keyring: keyring, Capabilities: NewMemoryCapabilityRepository(), Policy: testEgressPolicy(t), Fetcher: archives})
	runtime, err := issuer.IssueRuntime(ctx, "task")
	ref := runtime.ArchiveRef
	require.NoError(t, err)
	require.True(t, validArchiveReference(ref))
	require.NotContains(t, ref, "private")
	data, err := archives.ReadArchive(ctx, "wrong-session", ref)
	require.Error(t, err)
	require.Empty(t, data)
	data, err = archives.ReadArchive(ctx, "session", ref)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	require.NotContains(t, zr.File[0].Name, "private-name")
	r, err := zr.File[0].Open()
	require.NoError(t, err)
	contents, err := io.ReadAll(r)
	require.NoError(t, err)
	r.Close()
	require.Equal(t, "print('hello')", string(contents))
	require.NoError(t, repository.UpdateStatus(ctx, "task", tasks.StatusCancelled, time.Now()))
	_, err = archives.ReadArchive(ctx, "session", ref)
	require.Error(t, err)
}

func TestMCPArchiveRejectsTraversalSymlinkAndDuplicateEntries(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/windows", "a\\b", "a/../b", ".git/config"} {
		t.Run(name, func(t *testing.T) { require.False(t, safeArchiveName(name)) })
	}
	var source bytes.Buffer
	zw := zip.NewWriter(&source)
	for range 2 {
		w, _ := zw.Create("duplicate.py")
		_, _ = w.Write([]byte("x"))
	}
	require.NoError(t, zw.Close())
	builder := newSourceArchive()
	require.Error(t, builder.addZIP(bytes.NewReader(source.Bytes()), int64(source.Len()), ""))
}

func TestMCPGitFetcherRequiresControlledHTTPSPolicy(t *testing.T) {
	for _, target := range []string{"https://git.example.test/a.git", "http://git.example.test/a.git", "file:///tmp/a", "ssh://git@git.example.test/a.git"} {
		_, err := fetchGitArchive(context.Background(), nil, target)
		require.ErrorIs(t, err, ErrRuntimeUnavailable)
		require.NotContains(t, err.Error(), "example.test")
	}
}
