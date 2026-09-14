package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiops/internal/goldenset"
	"aiops/internal/phoenix"
	"aiops/internal/retrieve"
)

func TestConvertTraces(t *testing.T) {
	in := []retrieve.LinkTrace{
		{Kind: "recall", Impl: "hybrid", DurationMS: 5, Hit: true, Note: "n"},
		{Kind: "rerank", Impl: "none"},
	}
	out := convertTraces(in)
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0].Kind != "recall" || out[0].Impl != "hybrid" || out[0].DurationMS != 5 || !out[0].Hit || out[0].Note != "n" {
		t.Fatalf("out[0]=%+v", out[0])
	}
	if out[1].Kind != "rerank" || out[1].Impl != "none" {
		t.Fatalf("out[1]=%+v", out[1])
	}
}

func TestToOverall(t *testing.T) {
	r := VariantReport{
		Overall: Metrics{MRR: 0.5, NDCG: 0.6, Recall: 0.7, Precision: 0.8, RejectRate: 0.5},
		Items: []goldenset.ItemResult{
			{ID: "q1", Behavior: "hit"},
			{ID: "q2", Behavior: "reject", RejectOK: true},
			{ID: "q3", Behavior: "reject", RejectOK: false},
		},
	}
	o := toOverall(r)
	if o.MRR != 0.5 || o.NDCG != 0.6 || o.Recall != 0.7 || o.Precision != 0.8 {
		t.Fatalf("metrics=%+v", o)
	}
	if o.HitCount != 1 || o.RejectCount != 2 || o.RejectOK != 1 {
		t.Fatalf("counts=%+v", o)
	}
}

func TestFindRagDataset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/datasets" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"DS2","name":"other"},{"id":"DS1","name":"aiops-rag-golden"}]}`)
	}))
	defer srv.Close()
	id, err := findRagDataset(phoenix.NewClient(srv.URL, ""))
	if err != nil {
		t.Fatal(err)
	}
	if id != "DS1" {
		t.Fatalf("id=%q", id)
	}
}

func TestFindRagDatasetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"DS2","name":"other"}]}`)
	}))
	defer srv.Close()
	if _, err := findRagDataset(phoenix.NewClient(srv.URL, "")); err == nil {
		t.Fatal("未找到数据集应报错")
	}
}
