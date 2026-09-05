package tasks

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Juneoww/AIG_Custom/common/runner"
)

var safeModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

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
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// TaskInputSummary contains only display-safe, task-type-specific metadata.
// Raw content, raw params, credentials, URLs, attachment IDs and engine data
// are intentionally not representable by this type.
type TaskInputSummary struct {
	Language     string `json:"language,omitempty"`
	ModelID      string `json:"model_id,omitempty"`
	Thread       int    `json:"thread,omitempty"`
	Timeout      int    `json:"timeout,omitempty"`
	TargetCount  int    `json:"target_count,omitempty"`
	NumPrompts   int    `json:"num_prompts,omitempty"`
	PortScanMode string `json:"port_scan_mode,omitempty"`
}

type TaskDetail struct {
	ID           string           `json:"id"`
	Owner        string           `json:"owner"`
	TaskType     string           `json:"task_type"`
	Status       Status           `json:"status"`
	Remark       string           `json:"remark,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	InputSummary TaskInputSummary `json:"input_summary"`
}

// TaskCreateErrorResponse keeps a persisted task visible after dispatch
// failure without exposing engine or request internals.
type TaskCreateErrorResponse struct {
	Error string     `json:"error"`
	Task  TaskDetail `json:"task"`
}

// TaskCreateBadRequestResponse is the bounded wire form for rejected create requests.
type TaskCreateBadRequestResponse struct {
	Error string `json:"error"`
}

type taskDetailFields struct {
	ID, Owner, TaskType, Content, Remark, CountryIsoCode string
	Status                                               Status
	TargetCount                                          int
	Params                                               json.RawMessage
	CreatedAt, UpdatedAt                                 time.Time
}

func taskSummaryOf(task *Task) TaskSummary {
	return TaskSummary{
		ID: task.ID, Owner: task.OwnerUsername, TaskType: canonicalTaskType(task.TaskType), Status: task.Status,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

func taskDetailOf(task *Task) TaskDetail {
	return taskDetailFromFields(taskDetailFields{
		ID: task.ID, Owner: task.OwnerUsername, TaskType: task.TaskType, Status: task.Status,
		Content: task.Content, Remark: task.Remark, TargetCount: task.TargetCount,
		Params: task.Params, CountryIsoCode: task.CountryIsoCode,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	})
}

func taskDetailOfView(view View) TaskDetail {
	return taskDetailFromFields(taskDetailFields{
		ID: view.ID, Owner: view.OwnerUsername, TaskType: view.TaskType, Status: view.Status,
		Content: view.Content, Remark: view.Remark, TargetCount: view.TargetCount,
		Params: view.Params, CountryIsoCode: view.CountryIsoCode,
		CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	})
}

func taskDetailFromFields(fields taskDetailFields) TaskDetail {
	detail := TaskDetail{
		ID: fields.ID, Owner: fields.Owner, TaskType: canonicalTaskType(fields.TaskType), Status: fields.Status,
		CreatedAt: fields.CreatedAt, UpdatedAt: fields.UpdatedAt,
		InputSummary: safeInputSummary(&Task{
			TaskType: fields.TaskType, Content: fields.Content, TargetCount: fields.TargetCount,
			Params: fields.Params, CountryIsoCode: fields.CountryIsoCode,
		}),
	}
	if remark := safeTaskRemark(fields.Remark); remark != "" {
		detail.Remark = remark
	}
	return detail
}

func safeInputSummary(task *Task) TaskInputSummary {
	if task == nil {
		return TaskInputSummary{}
	}
	type displayParams struct {
		ModelID string `json:"model_id"`
		Thread  int    `json:"thread"`
		Timeout int    `json:"timeout"`
		Dataset struct {
			NumPrompts int `json:"numPrompts"`
		} `json:"dataset"`
	}
	var params displayParams
	_ = json.Unmarshal(task.Params, &params)
	switch canonicalTaskType(task.TaskType) {
	case "mcp_scan":
		return TaskInputSummary{
			Language: safeLanguage(task.CountryIsoCode),
			Thread:   safePositiveInt(params.Thread, 1024),
		}
	case "ai_infra_scan":
		targetCount := task.TargetCount
		if targetCount == 0 {
			targetCount = nonEmptyLineCount(task.Content)
			if targetCount > runner.MaxTargetExpressions {
				targetCount = 0
			}
		} else if targetCount < 1 || targetCount > runner.MaxTargetExpressions {
			targetCount = 0
		}
		summary := TaskInputSummary{
			Language:    safeLanguage(task.CountryIsoCode),
			ModelID:     safeModelID(params.ModelID),
			Timeout:     safePositiveInt(params.Timeout, 86400),
			TargetCount: targetCount,
		}
		if mode, valid := normalizedInfrastructurePortScanMode(task.Params); valid {
			summary.PortScanMode = string(mode)
		}
		return summary
	case "model_redteam_report":
		return TaskInputSummary{
			Language:   safeLanguage(task.CountryIsoCode),
			NumPrompts: safePositiveInt(params.Dataset.NumPrompts, 1_000_000),
		}
	case "agent_scan":
		return TaskInputSummary{Language: safeLanguage(task.CountryIsoCode)}
	default:
		return TaskInputSummary{}
	}
}

func safeTaskRemark(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxTaskRemarkRuneCount {
		return ""
	}
	return value
}

func safeModelID(value string) string {
	if len(value) == 0 || len(value) > 128 || !safeModelIDPattern.MatchString(value) {
		return ""
	}
	return value
}

func canonicalTaskType(value string) string {
	switch value {
	case "mcp_scan", "Mcp-Scan":
		return "mcp_scan"
	case "ai_infra_scan", "AI-Infra-Scan":
		return "ai_infra_scan"
	case "model_redteam_report", "Model-Redteam-Report":
		return "model_redteam_report"
	case "agent_scan", "Agent-Scan":
		return "agent_scan"
	default:
		return "unknown"
	}
}

func safeLanguage(value string) string {
	switch value {
	case "zh", "zh_CN":
		return "zh"
	case "en":
		return "en"
	default:
		return ""
	}
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
