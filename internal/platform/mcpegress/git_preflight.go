package mcpegress

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"io"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
)

// preflightGitPack 必须在 go-git Parser 初始化前执行。Parser 会按对象计数
// 分配索引，并直接创建 MemoryObject；存储 Writer hook 不能代替这里的限制。
func preflightGitPack(ctx context.Context, raw []byte) error {
	if len(raw) < 32 || int64(len(raw)) > maxArchiveBytes {
		return ErrRuntimeUnavailable
	}
	checksum := sha1.Sum(raw[:len(raw)-20])
	if !bytes.Equal(checksum[:], raw[len(raw)-20:]) {
		return ErrRuntimeUnavailable
	}
	scanner := packfile.NewScanner(bytes.NewReader(raw))
	defer scanner.Close()
	version, count, err := scanner.Header()
	if err != nil || version != 2 || count == 0 || count > maxArchiveFiles*3 {
		return ErrRuntimeUnavailable
	}
	var budget int64 = 2 * maxArchiveBytes
	for index := uint32(0); index < count; index++ {
		if ctx.Err() != nil {
			return ErrRuntimeUnavailable
		}
		header, err := scanner.NextObjectHeader()
		if err != nil || header.Length < 0 || header.Length > maxArchiveFileBytes || header.Length > budget {
			return ErrRuntimeUnavailable
		}
		budget -= header.Length
		var delta bytes.Buffer
		var target io.Writer = io.Discard
		isDelta := header.Type == plumbing.OFSDeltaObject || header.Type == plumbing.REFDeltaObject
		if isDelta {
			target = &delta
		}
		writer := &preflightWriter{target: target, remaining: header.Length}
		written, _, err := scanner.NextObject(writer)
		if err != nil || written != header.Length || writer.remaining != 0 {
			return ErrRuntimeUnavailable
		}
		if isDelta {
			expanded, err := validateGitDelta(delta.Bytes())
			if err != nil || expanded > budget {
				return ErrRuntimeUnavailable
			}
			budget -= expanded
		}
	}
	footer, err := scanner.Checksum()
	if err != nil || footer != plumbing.Hash(checksum) {
		return ErrRuntimeUnavailable
	}
	return nil
}

type preflightWriter struct {
	target    io.Writer
	remaining int64
}

func (writer *preflightWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.remaining {
		return 0, ErrRuntimeUnavailable
	}
	n, err := writer.target.Write(data)
	writer.remaining -= int64(n)
	return n, err
}

// validateGitDelta 同时检查源/目标大小及每条 copy/literal 指令的累计展开长度。
// 仅检查头部目标大小不足以防止底层 unsigned remaining 下溢。
func validateGitDelta(data []byte) (int64, error) {
	source, n := binary.Uvarint(data)
	if n <= 0 || source > uint64(maxArchiveFileBytes) {
		return 0, ErrRuntimeUnavailable
	}
	data = data[n:]
	target, n := binary.Uvarint(data)
	if n <= 0 || target > uint64(maxArchiveFileBytes) {
		return 0, ErrRuntimeUnavailable
	}
	data = data[n:]
	var total uint64
	for len(data) > 0 {
		op := data[0]
		data = data[1:]
		var length uint64
		if op&0x80 != 0 {
			var offset uint64
			for bit := uint(0); bit < 4; bit++ {
				if op&(1<<bit) != 0 {
					if len(data) == 0 {
						return 0, ErrRuntimeUnavailable
					}
					offset |= uint64(data[0]) << (8 * bit)
					data = data[1:]
				}
			}
			for bit := uint(0); bit < 3; bit++ {
				if op&(1<<(bit+4)) != 0 {
					if len(data) == 0 {
						return 0, ErrRuntimeUnavailable
					}
					length |= uint64(data[0]) << (8 * bit)
					data = data[1:]
				}
			}
			if length == 0 {
				length = 65536
			}
			if offset > source || length > source-offset {
				return 0, ErrRuntimeUnavailable
			}
		} else {
			length = uint64(op)
			if length == 0 || length > uint64(len(data)) {
				return 0, ErrRuntimeUnavailable
			}
			data = data[length:]
		}
		if total > target || length > target-total {
			return 0, ErrRuntimeUnavailable
		}
		total += length
	}
	if total != target {
		return 0, ErrRuntimeUnavailable
	}
	return int64(target), nil
}
