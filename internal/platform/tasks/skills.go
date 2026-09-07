package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Juneoww/AIG_Custom/internal/skillarchive"
)

type skillsTaskParams struct {
	ModelID string `json:"model_id"`
}

// validateReadySkill 在创建任务前校验已授权附件；只读校验，不向 Web 服务目录解压目标代码。
func (service *AttachmentService) validateReadySkill(ctx context.Context, ownerUserID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	attachment, err := service.repository.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	if attachment.OwnerUserID != ownerUserID {
		return ErrForbidden
	}
	if attachment.State != AttachmentStateReady || attachment.Size <= 0 || attachment.Size > skillarchive.MaxArchiveBytes ||
		!strings.EqualFold(filepath.Ext(attachment.OriginalName), ".zip") || !strings.EqualFold(filepath.Ext(attachment.StorageName), ".zip") {
		return ErrInvalid
	}
	filename, err := service.storagePath(attachment.StorageName)
	if err != nil {
		return err
	}
	before, err := os.Lstat(filename)
	if err != nil {
		return classifyAttachmentStorageError(err)
	}
	if !before.Mode().IsRegular() || before.Size() != attachment.Size {
		return ErrAttachmentStorage
	}
	file, err := service.openFile(filename)
	if err != nil {
		return classifyAttachmentStorageError(err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != attachment.Size {
		return ErrAttachmentStorage
	}
	if _, err = skillarchive.InspectReader(file, after.Size()); err != nil {
		return ErrInvalid
	}
	return ctx.Err()
}
