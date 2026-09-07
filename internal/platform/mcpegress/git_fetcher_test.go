package mcpegress

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/require"
)

type archiveGitSession struct {
	head plumbing.Hash
	pack []byte
	last *packp.UploadPackRequest
}

func (session *archiveGitSession) AdvertisedReferences() (*packp.AdvRefs, error) {
	return session.AdvertisedReferencesContext(context.Background())
}
func (session *archiveGitSession) AdvertisedReferencesContext(context.Context) (*packp.AdvRefs, error) {
	refs := packp.NewAdvRefs()
	refs.Head = &session.head
	_ = refs.Capabilities.Set(capability.Shallow)
	return refs, nil
}
func (session *archiveGitSession) UploadPack(_ context.Context, request *packp.UploadPackRequest) (*packp.UploadPackResponse, error) {
	session.last = request
	return packp.NewUploadPackResponseWithPackfile(request, io.NopCloser(bytes.NewReader(session.pack))), nil
}
func (*archiveGitSession) Close() error { return nil }

func testArchiveGitSession(t *testing.T, mode filemode.FileMode, name string) *archiveGitSession {
	t.Helper()
	store := memory.NewStorage()
	blob := store.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	writer, _ := blob.Writer()
	_, _ = writer.Write([]byte("print('snapshot')"))
	writer.Close()
	blobHash, err := store.SetEncodedObject(blob)
	require.NoError(t, err)
	tree := &object.Tree{Entries: []object.TreeEntry{{Name: name, Mode: mode, Hash: blobHash}}}
	encodedTree := store.NewEncodedObject()
	require.NoError(t, tree.Encode(encodedTree))
	treeHash, err := store.SetEncodedObject(encodedTree)
	require.NoError(t, err)
	signature := object.Signature{Name: "test", Email: "test@example.invalid", When: time.Unix(0, 0)}
	commit := &object.Commit{Author: signature, Committer: signature, TreeHash: treeHash, Message: "test"}
	encodedCommit := store.NewEncodedObject()
	require.NoError(t, commit.Encode(encodedCommit))
	head, err := store.SetEncodedObject(encodedCommit)
	require.NoError(t, err)
	var output bytes.Buffer
	_, err = packfile.NewEncoder(&output, store, false).Encode([]plumbing.Hash{head, treeHash, blobHash}, 0)
	require.NoError(t, err)
	return &archiveGitSession{head: head, pack: output.Bytes()}
}

func TestMCPGitArchiveDecodesRealPackAndRejectsSubmodules(t *testing.T) {
	session := testArchiveGitSession(t, filemode.Regular, "source.py")
	data, err := gitArchiveFromSession(context.Background(), session)
	require.NoError(t, err)
	require.Equal(t, packp.DepthCommits(1), session.last.Depth)
	require.Equal(t, []plumbing.Hash{session.head}, session.last.Wants)
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, archive.File, 1)
	require.Equal(t, "source.py", archive.File[0].Name)
	for _, entry := range []struct {
		mode filemode.FileMode
		name string
	}{{filemode.Submodule, "submodule"}, {filemode.Symlink, "link"}, {filemode.Regular, ".gitmodules"}} {
		_, err = gitArchiveFromSession(context.Background(), testArchiveGitSession(t, entry.mode, entry.name))
		require.ErrorIs(t, err, ErrRuntimeUnavailable)
	}
}

func TestMCPGitObjectWriterRejectsDecompressionBudgetOverflow(t *testing.T) {
	storage := newBoundedGitStorage()
	storage.budget = 8
	object := storage.NewEncodedObject()
	writer, err := object.Writer()
	require.NoError(t, err)
	_, err = io.Copy(writer, strings.NewReader("too much data"))
	require.ErrorIs(t, err, ErrRuntimeUnavailable)
}
