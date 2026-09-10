// datasetctl 把本地评测资产灌入 Phoenix 并回写实验结果。
// 用法：
//
//	datasetctl push [--dataset agents|rag|all] [--golden testdata/golden_set.jsonl]
//	datasetctl list
//	datasetctl experiment --dataset agents|rag --report <json> [--name <prefix>] [--config a,b]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

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
	case "experiment":
		cmdExperiment(c, os.Args[2:])
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
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
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

func validateExperimentArgs(dataset, report string) error {
	if dataset != "agents" && dataset != "rag" {
		return fmt.Errorf("--dataset 必填且为 agents|rag")
	}
	if report == "" {
		return fmt.Errorf("--report 必填")
	}
	return nil
}

func cmdExperiment(c *phoenix.Client, args []string) {
	fs := flag.NewFlagSet("experiment", flag.ContinueOnError)
	dataset := fs.String("dataset", "", "agents|rag")
	reportPath := fs.String("report", "", "报告 JSON 路径")
	name := fs.String("name", "", "实验名前缀")
	configs := fs.String("config", "", "逗号分隔，仅 agents；空=全部")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if err := validateExperimentArgs(*dataset, *reportPath); err != nil {
		log.Fatal(err)
	}
	b, err := os.ReadFile(*reportPath)
	if err != nil {
		log.Fatalf("读报告失败: %v", err)
	}
	switch *dataset {
	case "agents":
		var rep eval.Report
		if err := json.Unmarshal(b, &rep); err != nil {
			log.Fatalf("报告解析失败: %v", err)
		}
		dsID, err := findDataset(c, eval.AgentDatasetName)
		if err != nil {
			log.Fatal(err)
		}
		prefix := *name
		if prefix == "" {
			prefix = eval.AgentDatasetName
		}
		var filter []string
		if *configs != "" {
			filter = strings.Split(*configs, ",")
		}
		if err := eval.PushAgentExperiment(c, dsID, rep, filter, prefix); err != nil {
			log.Fatalf("回写失败: %v", err)
		}
		fmt.Printf("experiment 已回写: %s（%d configs）\n", prefix, len(rep.Meta.Configs))
	case "rag":
		log.Fatal("rag 回写将在后续任务提供")
	}
}

// findDataset 按名称查数据集 id。
func findDataset(c *phoenix.Client, name string) (string, error) {
	ds, err := c.ListDatasets()
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Name == name {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("未找到数据集 %s（先执行 datasetctl push）", name)
}
