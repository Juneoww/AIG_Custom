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
//
// Requirement: Any integration or derivative work must explicitly attribute
// Tencent Zhuque Lab (https://github.com/Tencent/AI-Infra-Guard) in its
// documentation or user interface, as detailed in the NOTICE file.

package main

import (
	"context"
	"log"
	"os"

	"github.com/Juneoww/AIG_Custom/cmd/cli/cmd"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "migrate" {
		if err := runMigrate(); err != nil {
			log.Fatalf("数据库迁移失败: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "bootstrap-admin" {
		if err := runBootstrapAdmin(); err != nil {
			log.Fatalf("初始化管理员失败: %v", err)
		}
		return
	}
	cmd.Execute()
}

func runMigrate() error {
	config, err := database.LoadConfigFromEnv()
	if err != nil {
		return err
	}
	db, err := database.InitDB(config)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	return database.Migrate(db)
}

func runBootstrapAdmin() error {
	config, err := database.LoadConfigFromEnv()
	if err != nil {
		return err
	}
	db, err := database.InitDB(config)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	repo := identity.NewGormRepository(db)
	if err := repo.Init(); err != nil {
		return err
	}
	password, err := identity.ReadBootstrapPassword(os.Stdin)
	if err != nil {
		return err
	}
	username := os.Getenv("AIG_BOOTSTRAP_ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}
	return identity.BootstrapAdmin(context.Background(), identity.NewService(repo), username, password)
}
