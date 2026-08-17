package tasks

import (
	"encoding/json"
	"strings"
	"time"
)

// TaskSummary is the intentionally small browser list wire model.
type TaskSummary struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	TaskType  string    `json:"task_type"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskListResponse struct {
	Items    []TaskSummary `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// TaskInputSummary contains only display-safe, task-type-specific metadata.
// Raw content, raw params, credentials, URLs, attachment IDs and engine data
// are intentionally not representable by this type.
type TaskInputSummary struct {
	AgentID      string   `json:"agent_id,omitempty"`
	Language     string   `json:"language,omitempty"`
	Thread       int      `json:"thread,omitempty"`
	Timeout      int      `json:"timeout,omitempty"`
	TargetCount  int      `json:"target_count,omitempty"`
	DatasetNames []string `json:"dataset_names,omitempty"`
	NumPrompts   int      `json:"num_prompts,omitempty"`
	Techniques   []string `json:"techniques,omitempty"`
}

type TaskDetail struct {
	ID           string           `json:"id"`
	Owner        string           `json:"owner"`
	TaskType     string           `json:"task_type"`
	Status       Status           `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	InputSummary TaskInputSummary `json:"input_summary"`
}

func taskSummaryOf(task *Task) TaskSummary {
	return TaskSummary{
		ID: task.ID, Owner: task.OwnerUsername, TaskType: task.TaskType, Status: task.Status,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

func taskDetailOf(task *Task) TaskDetail {
	summary := taskSummaryOf(task)
	return TaskDetail{
		ID: summary.ID, Owner: summary.Owner, TaskType: summary.TaskType, Status: summary.Status,
		CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt, InputSummary: safeInputSummary(task),
	}
}

func safeInputSummary(task *Task) TaskInputSummary {
	if task == nil {
		return TaskInputSummary{}
	}
	type displayParams struct {
		AgentID    string   `json:"agent_id"`
		Language   string   `json:"language"`
		Thread     int      `json:"thread"`
		Timeout    int      `json:"timeout"`
		Techniques []string `json:"techniques"`
		Dataset    struct {
			DataFile   []string `json:"dataFile"`
			NumPrompts int      `json:"numPrompts"`
		} `json:"dataset"`
	}
	var params displayParams
	_ = json.Unmarshal(task.Params, &params)
	taskType := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(task.TaskType), "-", "_"))
	switch taskType {
	case "mcp_scan":
		return TaskInputSummary{
			Language: safeDisplayString(params.Language),
			Thread:   safePositiveInt(params.Thread, 1024),
		}
	case "ai_infra_scan":
		return TaskInputSummary{
			Language:    safeDisplayString(task.CountryIsoCode),
			Timeout:     safePositiveInt(params.Timeout, 86400),
			TargetCount: nonEmptyLineCount(task.Content),
		}
	case "model_redteam_report":
		return TaskInputSummary{
			Language:     safeDisplayString(task.CountryIsoCode),
			DatasetNames: safeDisplayStrings(params.Dataset.DataFile, 20),
			NumPrompts:   safePositiveInt(params.Dataset.NumPrompts, 1_000_000),
			Techniques:   safeDisplayStrings(params.Techniques, 20),
		}
	case "agent_scan":
		return TaskInputSummary{
			AgentID:  safeDisplayString(params.AgentID),
			Language: safeDisplayString(task.CountryIsoCode),
		}
	case "model_jailbreak":
		return TaskInputSummary{Language: safeDisplayString(task.CountryIsoCode)}
	default:
		return TaskInputSummary{}
	}
}

func safeDisplayString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 || strings.ContainsAny(value, "\r\n\t") {
		return ""
	}
	normalized := strings.ToLower(value)
	for _, unsafe := range []string{"authorization", "bearer ", "password", "secret", "api_key", "apikey", "token"} {
		if strings.Contains(normalized, unsafe) {
			return ""
		}
	}
	if strings.HasPrefix(normalized, "sk-") || strings.Contains(normalized, "://") {
		return ""
	}
	return value
}

func safeDisplayStrings(values []string, limit int) []string {
	if len(values) == 0 || len(values) > limit {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = safeDisplayString(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func safePositiveInt(value, maximum int) int {
	if value < 1 || value > maximum {
		return 0
	}
	return value
}

func nonEmptyLineCount(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
