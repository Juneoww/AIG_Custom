// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package websocket

import (
	"fmt"

	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	platformbrand "github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	platformreports "github.com/Juneoww/AIG_Custom/internal/platform/reports"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"gorm.io/gorm"
)

var _ platformreports.DashboardRepository = (*platformreports.GormRepository)(nil)
var _ platformtasks.RecentRepository = (*platformtasks.GormRepository)(nil)

type runtimeDatastores struct {
	identityRepository      *identity.GormRepository
	auditRepository         *platformaudit.GormRepository
	platformModelRepository *platformmodels.GormRepository
	platformTaskRepository  *platformtasks.GormRepository
	reportRepository        *platformreports.GormRepository
	brandRepository         *platformbrand.GormRepository
	taskStore               *database.TaskStore
	modelStore              *database.ModelStore
	agentStore              *database.AgentStore
}

// initializeRuntimeDatastores is the single production runtime initialization
// path. It only validates an already migrated schema; `aig migrate` is the
// sole path allowed to apply database DDL.
func initializeRuntimeDatastores(db *gorm.DB) (*runtimeDatastores, error) {
	if err := database.ValidateRuntimeSchema(db); err != nil {
		return nil, err
	}

	stores := &runtimeDatastores{
		identityRepository:      identity.NewGormRepository(db),
		auditRepository:         platformaudit.NewGormRepository(db),
		platformModelRepository: platformmodels.NewGormRepository(db),
		platformTaskRepository:  platformtasks.NewGormRepository(db),
		reportRepository:        platformreports.NewGormRepository(db),
		brandRepository:         platformbrand.NewGormRepository(db),
		taskStore:               database.NewTaskStore(db),
		modelStore:              database.NewModelStore(db),
		agentStore:              database.NewAgentStore(db),
	}
	initializers := []struct {
		name string
		init func() error
	}{
		{name: "identity", init: stores.identityRepository.Init},
		{name: "audit", init: stores.auditRepository.Init},
		{name: "platform models", init: stores.platformModelRepository.Init},
		{name: "platform tasks", init: stores.platformTaskRepository.Init},
		{name: "reports", init: stores.reportRepository.Init},
		{name: "brand", init: stores.brandRepository.Init},
		{name: "tasks", init: stores.taskStore.Init},
		{name: "models", init: stores.modelStore.Init},
		{name: "agents", init: stores.agentStore.Init},
	}
	for _, initializer := range initializers {
		if err := initializer.init(); err != nil {
			return nil, fmt.Errorf("初始化%s数据存储失败: %w", initializer.name, err)
		}
	}
	return stores, nil
}
