package mcpegress

import (
	"encoding/binary"
	"io"
)

// preflightZIPDirectory 在标准库为每个 ZIP 项分配对象前，逐项验证中央目录。
// 本受限代码快照不需要 ZIP64/多卷/自解压格式；拒绝歧义偏移及不一致计数。
func preflightZIPDirectory(reader io.ReaderAt, size int64) error {
	if size < 22 || size > maxArchiveBytes {
		return ErrRuntimeUnavailable
	}
	tailSize := int64(22 + 65535)
	if size < tailSize {
		tailSize = size
	}
	tail := make([]byte, int(tailSize))
	if _, err := reader.ReadAt(tail, size-tailSize); err != nil {
		return ErrRuntimeUnavailable
	}
	endIndex := -1
	for index := len(tail) - 22; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:]) == 0x06054b50 {
			// 标准库会选择最后的签名；不能跳过注释内的歧义 EOCD 再选更早记录。
			if index+22+int(binary.LittleEndian.Uint16(tail[index+20:])) != len(tail) {
				return ErrRuntimeUnavailable
			}
			endIndex = index
			break
		}
	}
	if endIndex < 0 {
		return ErrRuntimeUnavailable
	}
	end := tail[endIndex:]
	entries := int(binary.LittleEndian.Uint16(end[10:]))
	if binary.LittleEndian.Uint16(end[4:]) != 0 || binary.LittleEndian.Uint16(end[6:]) != 0 || int(binary.LittleEndian.Uint16(end[8:])) != entries || entries > maxArchiveFiles {
		return ErrRuntimeUnavailable
	}
	directorySize := int64(binary.LittleEndian.Uint32(end[12:]))
	offset := int64(binary.LittleEndian.Uint32(end[16:]))
	endOffset := size - tailSize + int64(endIndex)
	// 限制中央目录元数据总量；也排除 ZIP64 扩展、签名和偏移修正空间。
	if directorySize == 0xffff || directorySize > 8<<20 || offset > endOffset || directorySize != endOffset-offset {
		return ErrRuntimeUnavailable
	}
	count := 0
	for offset < endOffset {
		if count >= maxArchiveFiles || endOffset-offset < 46 {
			return ErrRuntimeUnavailable
		}
		var header [46]byte
		if _, err := reader.ReadAt(header[:], offset); err != nil || binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return ErrRuntimeUnavailable
		}
		length := int64(46) + int64(binary.LittleEndian.Uint16(header[28:])) + int64(binary.LittleEndian.Uint16(header[30:])) + int64(binary.LittleEndian.Uint16(header[32:]))
		if length > endOffset-offset || binary.LittleEndian.Uint16(header[34:]) != 0 {
			return ErrRuntimeUnavailable
		}
		offset += length
		count++
	}
	if count != entries {
		return ErrRuntimeUnavailable
	}
	return nil
}
