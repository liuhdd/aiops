package phoenix

import (
	"net/http"
	"net/url"
	"time"
)

type Experiment struct {
	ID, DatasetID, DatasetVersionID, Name string
}

// RunRequest 一条实验 run（对应一个 dataset example 的一次执行）。
type RunRequest struct {
	DatasetExampleID string    `json:"dataset_example_id"`
	Output           any       `json:"output"`
	RepetitionNumber int       `json:"repetition_number"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	TraceID          string    `json:"trace_id,omitempty"`
	Error            string    `json:"error,omitempty"`
}

type EvaluationResult struct {
	Label       string   `json:"label,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Explanation string   `json:"explanation,omitempty"`
}

type EvaluationRequest struct {
	ExperimentRunID string            `json:"experiment_run_id"`
	Name            string            `json:"name"`
	AnnotatorKind   string            `json:"annotator_kind"` // LLM | CODE | HUMAN
	StartTime       time.Time         `json:"start_time"`
	EndTime         time.Time         `json:"end_time"`
	Result          *EvaluationResult `json:"result,omitempty"`
	Error           string            `json:"error,omitempty"`
	Metadata        map[string]any    `json:"metadata,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
}

// Writer 实验回写所需的最小客户端接口（*Client 实现），便于 fake 注入测试。
type Writer interface {
	ListExamples(datasetID, versionID string) ([]Example, error)
	CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (Experiment, error)
	LogRun(experimentID string, r RunRequest) (string, error)
	LogEvaluation(e EvaluationRequest) (string, error)
}

func (c *Client) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (Experiment, error) {
	body := map[string]any{"name": name, "repetitions": repetitions}
	if metadata != nil {
		body["metadata"] = metadata
	}
	var out struct {
		Data struct {
			ID               string `json:"id"`
			DatasetID        string `json:"dataset_id"`
			DatasetVersionID string `json:"dataset_version_id"`
			Name             string `json:"name"`
		} `json:"data"`
	}
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/experiments"
	if err := c.do(http.MethodPost, path, body, &out, uploadTimeout); err != nil {
		return Experiment{}, err
	}
	return Experiment{
		ID: out.Data.ID, DatasetID: out.Data.DatasetID,
		DatasetVersionID: out.Data.DatasetVersionID, Name: out.Data.Name,
	}, nil
}

func (c *Client) LogRun(experimentID string, r RunRequest) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	path := "/v1/experiments/" + url.PathEscape(experimentID) + "/runs"
	if err := c.do(http.MethodPost, path, r, &out, uploadTimeout); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", &HTTPError{StatusCode: http.StatusOK, Body: "log run 响应缺 id"}
	}
	return out.Data.ID, nil
}

func (c *Client) LogEvaluation(e EvaluationRequest) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(http.MethodPost, "/v1/experiment_evaluations", e, &out, uploadTimeout); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", &HTTPError{StatusCode: http.StatusOK, Body: "log evaluation 响应缺 id"}
	}
	return out.Data.ID, nil
}
