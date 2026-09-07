package mcpegress

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/require"
)

func rawSingleObjectPack(t *testing.T, kind plumbing.ObjectType, declared uint64, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	output.WriteString("PACK")
	_ = binary.Write(&output, binary.BigEndian, uint32(2))
	_ = binary.Write(&output, binary.BigEndian, uint32(1))
	first := byte(kind)<<4 | byte(declared&15)
	declared >>= 4
	if declared > 0 {
		first |= 128
	}
	output.WriteByte(first)
	for declared > 0 {
		next := byte(declared & 127)
		declared >>= 7
		if declared > 0 {
			next |= 128
		}
		output.WriteByte(next)
	}
	if kind == plumbing.REFDeltaObject {
		output.Write(make([]byte, 20))
	}
	w := zlib.NewWriter(&output)
	_, err := w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	checksum := sha1.Sum(output.Bytes())
	output.Write(checksum[:])
	return output.Bytes()
}

func TestMCPGitPreflightRejectsCountInflationAndDeltaOverflow(t *testing.T) {
	count := []byte("PACK\x00\x00\x00\x02\xff\xff\xff\xff")
	countChecksum := sha1.Sum(count)
	count = append(count, countChecksum[:]...)
	require.Error(t, preflightGitPack(context.Background(), count))
	for _, pack := range [][]byte{
		rawSingleObjectPack(t, plumbing.BlobObject, uint64(maxArchiveFileBytes+1), []byte("x")),
		rawSingleObjectPack(t, plumbing.BlobObject, 1, bytes.Repeat([]byte("x"), 1<<20)),
		rawSingleObjectPack(t, plumbing.REFDeltaObject, 6, []byte{3, 3, 0x90, 3, 0x90, 3}),
	} {
		require.Error(t, preflightGitPack(context.Background(), pack))
	}
	require.NoError(t, preflightGitPack(context.Background(), rawSingleObjectPack(t, plumbing.BlobObject, 3, []byte("abc"))))
}
