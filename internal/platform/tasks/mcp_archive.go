package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// OpenMCPAttached 仅供平台归档器读取已绑定的 MCP 附件，不注册浏览器路由，
// 不返回用户文件名或存储路径。owner 必须来自已授权任务的持久化所有者。
func (service *AttachmentService) OpenMCPAttached(ctx context.Context, owner, id string) (*os.File, string, error) {
	if !service.config.MCPOnly {
		return nil, "", ErrForbidden
	}
	attachment, err := service.scopedAttachment(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if attachment.OwnerUserID != owner || attachment.State != AttachmentStateAttached {
		return nil, "", ErrNotFound
	}
	filename, err := service.storagePath(attachment.StorageName)
	if err != nil {
		return nil, "", ErrAttachmentStorage
	}
	before, err := os.Lstat(filename)
	if err != nil || !before.Mode().IsRegular() {
		return nil, "", ErrAttachmentStorage
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, "", ErrAttachmentStorage
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != attachment.Size {
		file.Close()
		return nil, "", ErrAttachmentStorage
	}
	return file, strings.ToLower(filepath.Ext(attachment.StorageName)), nil
}
