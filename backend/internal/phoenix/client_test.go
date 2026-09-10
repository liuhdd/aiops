package phoenix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadDatasetPostsParallelArrays(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/datasets/upload" || r.URL.Query().Get("sync") != "true" {
			t.Errorf("unexpected: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"data":{"dataset_id":"DS1","version_id":"V1","num_created_examples":2,"num_updated_examples":0}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	res, err := c.UploadDataset("create", "ds", "desc", []Example{
		{ID: "a", Input: map[string]any{"q": 1}, Output: map[string]any{"a": 2}, Metadata: map[string]any{"m": 1}, Splits: []string{"s1"}},
		{ID: "b", Input: map[string]any{"q": 2}, Output: map[string]any{"a": 3}, Metadata: map[string]any{"m": 2}, Splits: []string{"s2"}},
	})
	if err != nil {
		t.Fatalf("UploadDataset: %v", err)
	}
	if res.DatasetID != "DS1" || res.VersionID != "V1" || res.NumCreated != 2 {
		t.Fatalf("res = %+v", res)
	}
	if got["action"] != "create" || got["name"] != "ds" {
		t.Fatalf("body = %v", got)
	}
	if ids := got["example_ids"].([]any); ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("example_ids = %v", ids)
	}
	if ins := got["inputs"].([]any); ins[0].(map[string]any)["q"].(float64) != 1 {
		t.Fatalf("inputs = %v", ins)
	}
	if sp := got["splits"].([]any); sp[0].([]any)[0] != "s1" {
		t.Fatalf("splits = %v", sp)
	}
}

func TestUploadDataset409IsConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, "exists")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	_, err := c.UploadDataset("create", "ds", "", []Example{{ID: "a", Input: map[string]any{}, Output: map[string]any{}}})
	if !IsConflict(err) {
		t.Fatalf("应可判别 409, got %v", err)
	}
	if !strings.Contains(err.Error(), "409") {
		t.Fatalf("err 应含状态码, got %v", err)
	}
}

func TestListDatasetsAndExamples(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/datasets":
			_, _ = io.WriteString(w, `{"data":[{"id":"DS1","name":"ds"}]}`)
		case "/v1/datasets/DS1/examples":
			_, _ = io.WriteString(w, `{"data":{"dataset_id":"DS1","version_id":"V1","examples":[{"id":"EX1","node_id":"N1","input":{"q":1},"output":{"a":2},"metadata":{"task_id":"t1"}}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	ds, err := c.ListDatasets()
	if err != nil || len(ds) != 1 || ds[0].Name != "ds" || ds[0].ID != "DS1" {
		t.Fatalf("ListDatasets = (%v,%v)", ds, err)
	}
	exs, err := c.ListExamples("DS1", "")
	if err != nil || len(exs) != 1 || exs[0].ID != "EX1" || exs[0].Metadata["task_id"] != "t1" {
		t.Fatalf("ListExamples = (%v,%v)", exs, err)
	}
}

func TestSendsBearerWhenAPIKeySet(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "sk-x")
	if _, err := c.ListDatasets(); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-x" {
		t.Fatalf("auth = %q", auth)
	}
}
