package mcpegress

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingArchiveReader struct {
	source *bytes.Reader
	bytes  int
}

func (reader *countingArchiveReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := reader.source.ReadAt(p, off)
	reader.bytes += n
	return n, err
}

func TestZIPPreflightLimitsRealDirectoryEntriesBeforeAllocation(t *testing.T) {
	// EOCD 的 16 位计数看似只有 4,464，但真实中央目录有 70,000 条。
	var raw bytes.Buffer
	for range 70000 {
		header := make([]byte, 47)
		binary.LittleEndian.PutUint32(header, 0x02014b50)
		binary.LittleEndian.PutUint16(header[28:], 1)
		header[46] = 'a'
		raw.Write(header)
	}
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], 4464)
	binary.LittleEndian.PutUint16(end[10:], 4464)
	binary.LittleEndian.PutUint32(end[12:], uint32(raw.Len()))
	raw.Write(end)
	reader := &countingArchiveReader{source: bytes.NewReader(raw.Bytes())}
	require.ErrorIs(t, preflightZIPDirectory(reader, int64(raw.Len())), ErrRuntimeUnavailable)
	require.Less(t, reader.bytes, 700000, "must stop before decoding unbounded directory entries")
}

func TestZIPPreflightRejectsMalformedAndZIP64Boundaries(t *testing.T) {
	for _, raw := range [][]byte{nil, make([]byte, 22), []byte("PK\x05\x06")} {
		require.Error(t, preflightZIPDirectory(bytes.NewReader(raw), int64(len(raw))))
	}
	require.Error(t, preflightZIPDirectory(io.NewSectionReader(bytes.NewReader(nil), 0, 0), maxArchiveBytes+1))
}

func TestZIPPreflightRejectsAmbiguousEOCDAndZIP64Sentinel(t *testing.T) {
	ambiguous := make([]byte, 45)
	binary.LittleEndian.PutUint32(ambiguous, 0x06054b50)
	binary.LittleEndian.PutUint16(ambiguous[20:], 23)
	binary.LittleEndian.PutUint32(ambiguous[22:], 0x06054b50)
	require.Error(t, preflightZIPDirectory(bytes.NewReader(ambiguous), int64(len(ambiguous))))
	var raw bytes.Buffer
	for _, length := range []int{32767, 32768} {
		header := make([]byte, length)
		binary.LittleEndian.PutUint32(header, 0x02014b50)
		binary.LittleEndian.PutUint16(header[28:], 1)
		binary.LittleEndian.PutUint16(header[30:], uint16(length-47))
		header[46] = 'a'
		raw.Write(header)
	}
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], 2)
	binary.LittleEndian.PutUint16(end[10:], 2)
	binary.LittleEndian.PutUint32(end[12:], 0xffff)
	raw.Write(end)
	require.Error(t, preflightZIPDirectory(bytes.NewReader(raw.Bytes()), int64(raw.Len())))
}
