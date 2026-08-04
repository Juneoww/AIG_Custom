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

package database

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const postgresDriver = "postgres"

// Config 用于保存 PostgreSQL 数据库配置。
type Config struct {
	Driver          string
	DSN             string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
}

// NewConfig 创建使用 PostgreSQL 的数据库配置。
func NewConfig(dsn string) *Config {
	return &Config{
		Driver:          postgresDriver,
		DSN:             dsn,
		MaxIdleConns:    10,
		MaxOpenConns:    25,
		ConnMaxLifetime: time.Hour,
	}
}

// LoadConfigFromEnv 从环境变量加载 PostgreSQL 配置。
func LoadConfigFromEnv() (*Config, error) {
	config := NewConfig(os.Getenv("DB_DSN"))
	if driver := os.Getenv("DB_DRIVER"); driver != "" {
		config.Driver = driver
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// Validate 检查当前交付支持的数据库配置。
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("数据库配置不能为空")
	}
	if strings.ToLower(c.Driver) != postgresDriver {
		return fmt.Errorf("不支持的数据库驱动 %q：当前仅支持 postgres", c.Driver)
	}
	if strings.TrimSpace(c.DSN) == "" {
		return fmt.Errorf("DB_DSN 不能为空")
	}
	return nil
}

// InitDB 用 GORM 初始化 PostgreSQL 连接并返回 *gorm.DB。
func InitDB(config *Config) (*gorm.DB, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	db, err := gorm.Open(postgres.Open(config.DSN), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		return nil, fmt.Errorf("打开 PostgreSQL 数据库失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取 PostgreSQL 连接池失败: %w", err)
	}
	if config.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(config.MaxIdleConns)
	}
	if config.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	}
	if config.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	}

	return db, nil
}
