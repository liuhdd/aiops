// promptctl 本地默认提示词与 Phoenix Prompt Hub 的灌库/核对工具。
// 用法：promptctl push  把 14 条默认模板灌到 Phoenix（已存在的 name 新建版本）
//
//	promptctl list  列出远端 prompt 核对灌库结果
package main

import (
	"fmt"
	"log"
	"os"
	"sort"

	"aiops/internal/config"
	"aiops/internal/prompt"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("用法：promptctl push|list")
	}
	cfg := config.Load()
	if cfg.PhoenixBaseURL == "" {
		log.Fatal("PHOENIX_BASE_URL 未配置")
	}
	f := prompt.NewPhoenixFetcher(cfg.PhoenixBaseURL, cfg.PhoenixTag, cfg.PhoenixAPIKey)
	switch os.Args[1] {
	case "push":
		names := make([]string, 0, len(prompt.All))
		for n := range prompt.All {
			names = append(names, n)
		}
		sort.Strings(names)
		failed := 0
		for _, n := range names {
			e := prompt.All[n]
			if err := f.Push(n, e.Role, e.Default, cfg.LLMModel); err != nil {
				log.Printf("push %s 失败: %v", n, err)
				failed++
				continue
			}
			fmt.Println("pushed:", n)
		}
		if failed > 0 { // 部分成功也算失败退出，便于 CI 感知
			os.Exit(1)
		}
	case "list":
		ps, err := f.List()
		if err != nil {
			log.Fatal(err)
		}
		for _, p := range ps {
			fmt.Printf("%s\t%s\n", p.Name, p.ID)
		}
	default:
		log.Fatalf("未知子命令 %q", os.Args[1])
	}
}
