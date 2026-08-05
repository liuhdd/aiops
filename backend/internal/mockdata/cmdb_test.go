package mockdata

import "testing"

func TestDevicesContainExpected(t *testing.T) {
	devs := Devices()
	if len(devs) != 6 {
		t.Fatalf("len(devices) = %d, want 6", len(devs))
	}
	names := map[string]bool{}
	for _, d := range devs {
		names[d.Name] = true
	}
	for _, want := range []string{"compute-01", "compute-02", "web-01", "db-01", "storage-01", "switch-core-01"} {
		if !names[want] {
			t.Fatalf("missing device %s", want)
		}
	}
}

func TestNeighbors(t *testing.T) {
	nb := Neighbors("compute-01")
	if len(nb) != 1 || nb[0].Name != "switch-core-01" {
		t.Fatalf("compute-01 neighbors = %+v", nb)
	}
	nb2 := Neighbors("switch-core-01")
	if len(nb2) != 5 {
		t.Fatalf("switch neighbors = %d, want 5", len(nb2))
	}
}

func TestRunbooks(t *testing.T) {
	rb, ok := SearchRunbook("gpu_util")
	if !ok || len(rb) == 0 {
		t.Fatalf("runbook for gpu_util missing")
	}
}
