package tasks

import "os"

// 仅在持有上传资源锁且数据库仍为 uploading 时清理本上传的崩溃残留。
func removeMCPMergeOrphan(filename string) error {
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrAttachmentStorage
	}
	if os.Remove(filename) != nil {
		return ErrAttachmentStorage
	}
	return nil
}
