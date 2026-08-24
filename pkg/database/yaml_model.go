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
	"errors"
	"io"
	"os"

	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"gopkg.in/yaml.v3"
)

const YamlModelPath = "db/model.yaml"

const (
	// Browser catalog loading is deliberately bounded because this legacy file
	// is parsed as one document and merged into an in-memory response catalog.
	maxYAMLModelBytes   int64 = 2 << 20
	maxYAMLModelEntries       = 1000
)

var (
	ErrYAMLModelsRead     = errors.New("YAML模型配置读取失败")
	ErrYAMLModelsInvalid  = errors.New("YAML模型配置解析失败")
	ErrYAMLModelsTooLarge = errors.New("YAML模型配置超出大小限制")
	ErrYAMLModelsTooMany  = errors.New("YAML模型配置条目过多")
)

// LoadYamlModels 加载YAML模型配置
func (s *ModelStore) LoadYamlModels() ([]*Model, error) {
	info, err := os.Stat(YamlModelPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		gologger.Errorf("读取模型配置文件失败")
		return nil, ErrYAMLModelsRead
	}
	if info.Size() > maxYAMLModelBytes {
		return nil, ErrYAMLModelsTooLarge
	}
	file, err := os.Open(YamlModelPath)
	if err != nil {
		gologger.Errorf("读取模型配置文件失败")
		return nil, ErrYAMLModelsRead
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxYAMLModelBytes+1))
	if err != nil {
		gologger.Errorf("读取模型配置文件失败")
		return nil, ErrYAMLModelsRead
	}
	if int64(len(data)) > maxYAMLModelBytes {
		return nil, ErrYAMLModelsTooLarge
	}

	var models []*Model
	if err := yaml.Unmarshal(data, &models); err != nil {
		gologger.Errorf("解析模型配置文件失败")
		return nil, ErrYAMLModelsInvalid
	}
	if len(models) > maxYAMLModelEntries {
		return nil, ErrYAMLModelsTooMany
	}

	return models, nil
}

// GetYamlModel 获取指定的YAML模型
func (s *ModelStore) GetYamlModel(modelID string) *Model {
	models, err := s.LoadYamlModels()
	if err != nil {
		return nil
	}
	for _, m := range models {
		if m.ModelID == modelID {
			return m
		}
	}
	return nil
}
