// datasetctl 把本地评测资产灌入 Phoenix 并回写实验结果。
// 用法：
//
//	datasetctl push [--dataset agents|rag|all] [--golden testdata/golden_set.jsonl]
//	datasetctl list
//	datasetctl experiment --dataset agents|rag --report <json> [--name <prefix>] [--config a,b]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"aiops/internal/config"
	"aiops/internal/eval"
	"aiops/internal/goldenset"
	"aiops/internal/phoenix"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cfg := config.Load()
	if cfg.PhoenixBaseURL == "" {
		log.Fatal("PHOENIX_BASE_URL 未配置")
	}
	c := phoenix.NewClient(cfg.PhoenixBaseURL, cfg.PhoenixAPIKey)
	switch os.Args[1] {
	case "push":
		cmdPush(c, os.Args[2:])
	case "list":
		cmdList(c)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  datasetctl push [--dataset agents|rag|all] [--golden testdata/golden_set.jsonl]
  datasetctl list
  datasetctl experiment --dataset agents|rag --report <json> [--name <prefix>] [--config a,b]`)
}

func cmdPush(c *phoenix.Client, args []string) {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	which := fs.String("dataset", "all", "agents|rag|all")
	goldenPath := fs.String("golden", "testdata/golden_set.jsonl", "RAG 标注集路径")
	fs.Parse(args)
	if *which == "agents" || *which == "all" {
		res, err := pushDataset(c, eval.AgentDatasetName, eval.AgentDatasetExamples())
		if err != nil {
			log.Fatalf("push %s 失败: %v", eval.AgentDatasetName, err)
		}
		fmt.Printf("pushed %s: dataset_id=%s created=%d updated=%d\n",
			eval.AgentDatasetName, res.DatasetID, res.NumCreated, res.NumUpdated)
	}
	if *which == "rag" || *which == "all" {
		items, err := goldenset.Load(*goldenPath)
		if err != nil {
			log.Fatalf("读取标注集失败: %v", err)
		}
		res, err := pushDataset(c, goldenset.RagDatasetName, goldenset.ToPhoenixExamples(items))
		if err != nil {
			log.Fatalf("push %s 失败: %v", goldenset.RagDatasetName, err)
		}
		fmt.Printf("pushed %s: dataset_id=%s created=%d updated=%d\n",
			goldenset.RagDatasetName, res.DatasetID, res.NumCreated, res.NumUpdated)
	}
}

func cmdList(c *phoenix.Client) {
	ds, err := c.ListDatasets()
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range ds {
		fmt.Printf("%s\t%s\n", d.Name, d.ID)
	}
}

// pushDataset 幂等上传：先 create，遇 409 退 update（依赖 example_ids 原地 upsert）。
func pushDataset(u phoenix.Uploader, name string, exs []phoenix.Example) (phoenix.UploadResult, error) {
	res, err := u.UploadDataset("create", name, "", exs)
	if phoenix.IsConflict(err) {
		return u.UploadDataset("update", name, "", exs)
	}
	return res, err
}
