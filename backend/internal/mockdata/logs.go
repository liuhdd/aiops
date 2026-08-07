package mockdata

import (
	"strings"
	"time"

	"aiops/internal/model"
)

func generateLogs(now time.Time) {
	logCache = map[string][]model.LogEntry{}
	entries := map[string][]struct {
		minutesAgo int
		level      string
		content    string
	}{
		"compute-01": {
			{45, "INFO", "load average: 12.5, 11.8, 10.2"},
			{20, "WARN", "cpu usage threshold exceeded 85%"},
			{10, "WARN", "scheduler stalled for 2.3s, cpu saturated"},
		},
		"db-01": {
			{90, "WARN", "df: /data usage 91%"},
			{40, "ERROR", "ext4-fs error (device sda1): no space left on device"},
			{15, "ERROR", "mysqld: InnoDB: cannot allocate page for the tablespace"},
		},
		"compute-02": {
			{60, "INFO", "NVRM: GPU at PCI 0000:3b:00.0 busy"},
			{35, "ERROR", "NVRM: Xid (PCI:0000:3b:00): 79, GPU has fallen off the bus"},
			{30, "ERROR", "nvidia-smi: no devices were found"},
			{28, "WARN", "NVLink: link down on GPU 2"},
		},
		"web-01": {
			{50, "WARN", "java: OutOfMemoryError: Java heap space"},
			{25, "ERROR", "kworker: page allocation failure, order:3"},
			{18, "WARN", "memcg: oom-kill process pid=1234"},
		},
		"switch-core-01": {
			{55, "INFO", "interface Eth1/1: link up"},
			{22, "ERROR", "interface Eth1/1: link down"},
			{20, "WARN", "ethernet: optical transceiver RX power low"},
			{15, "ERROR", "interface Eth1/1: link flapping detected"},
		},
	}
	for dev, list := range entries {
		var ls []model.LogEntry
		for _, e := range list {
			ls = append(ls, model.LogEntry{
				Time:    now.Add(-time.Duration(e.minutesAgo) * time.Minute),
				Source:  dev,
				Level:   e.level,
				Content: e.content,
			})
		}
		logCache[dev] = ls
	}
}

type LogEntryView struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Content string `json:"content"`
}

func ToLogViews(logs []model.LogEntry) []LogEntryView {
	out := make([]LogEntryView, 0, len(logs))
	for _, e := range logs {
		out = append(out, LogEntryView{Time: e.Time.Format("15:04"), Level: e.Level, Content: e.Content})
	}
	return out
}

func SearchLogs(device string, keywords []string, from, to time.Time) ([]model.LogEntry, error) {
	mu.RLock()
	defer mu.RUnlock()
	var out []model.LogEntry
	for _, e := range logCache[device] {
		if !((e.Time.Equal(from) || e.Time.After(from)) && (e.Time.Equal(to) || e.Time.Before(to))) {
			continue
		}
		if len(keywords) == 0 {
			out = append(out, e)
			continue
		}
		match := true
		for _, k := range keywords {
			if !strings.Contains(e.Content, k) {
				match = false
				break
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out, nil
}
