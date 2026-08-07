package mockdata

import "testing"

func TestDocsCoverMetricsAndDevices(t *testing.T) {
	docs := Docs()
	if len(docs) < 8 {
		t.Fatalf("docs = %d, want >= 8", len(docs))
	}
	metrics := map[string]bool{}
	devTypes := map[string]bool{}
	for _, d := range docs {
		for _, m := range d.Metrics {
			metrics[m] = true
		}
		for _, dt := range d.DeviceTypes {
			devTypes[dt] = true
		}
		if d.ID == "" || d.Title == "" || d.Body == "" {
			t.Fatalf("doc incomplete: %+v", d)
		}
	}
	for _, m := range []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"} {
		if !metrics[m] {
			t.Fatalf("metric %s not covered", m)
		}
	}
	for _, dt := range []string{"gpu-server", "server", "switch"} {
		if !devTypes[dt] {
			t.Fatalf("device type %s not covered", dt)
		}
	}
}
