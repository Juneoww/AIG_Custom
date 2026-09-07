package reports

import (
	"context"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

// TaskReportID 查询不可变快照，复用报告读取权限；未生成时不构造报告标识。
func (service *Service) TaskReportID(ctx context.Context, subject identity.Subject, taskID string) (string, error) {
	if !reportReader(subject) {
		return "", ErrForbidden
	}
	snapshot, err := service.repository.GetByTaskID(ctx, taskID)
	if err != nil {
		return "", err
	}
	if snapshot.TaskID != taskID || subject.Role == identity.RoleUser && snapshot.OwnerUserID != subject.UserID {
		return "", ErrNotFound
	}
	return snapshot.ID, nil
}
