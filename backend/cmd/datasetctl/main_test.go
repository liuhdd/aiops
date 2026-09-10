package main

import (
	"errors"
	"testing"

	"aiops/internal/phoenix"
)

type fakeUploader struct {
	calls []string
	fail  error
}

func (f *fakeUploader) UploadDataset(action, name, description string, exs []phoenix.Example) (phoenix.UploadResult, error) {
	f.calls = append(f.calls, action)
	if action == "create" && f.fail != nil {
		return phoenix.UploadResult{}, f.fail
	}
	return phoenix.UploadResult{DatasetID: "DS", VersionID: "V", NumCreated: len(exs)}, nil
}

func TestPushDatasetRetriesWithUpdateOnConflict(t *testing.T) {
	u := &fakeUploader{fail: &phoenix.HTTPError{StatusCode: 409, Body: "exists"}}
	_, err := pushDataset(u, "ds", []phoenix.Example{{ID: "a"}})
	if err != nil {
		t.Fatalf("应回退 update 成功, got %v", err)
	}
	if len(u.calls) != 2 || u.calls[0] != "create" || u.calls[1] != "update" {
		t.Fatalf("calls = %v", u.calls)
	}
}

func TestPushDatasetPropagatesNonConflict(t *testing.T) {
	u := &fakeUploader{fail: errors.New("boom")}
	if _, err := pushDataset(u, "ds", []phoenix.Example{{ID: "a"}}); err == nil {
		t.Fatal("非 409 错误应上抛")
	}
	if len(u.calls) != 1 {
		t.Fatalf("不应重试, calls = %v", u.calls)
	}
}
