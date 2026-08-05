package mockdata

import "aiops/internal/model"

var devices = []model.DeviceInfo{
	{Name: "compute-01", Model: "G6-GPU-8U", Rack: "A01", Zone: "ZoneA", Role: "gpu-server",
		Notes: []string{"GPU训练节点", "8xH800"}},
	{Name: "compute-02", Model: "G6-GPU-8U", Rack: "A02", Zone: "ZoneA", Role: "gpu-server",
		Notes: []string{"GPU推理节点", "8xA100"}},
	{Name: "web-01", Model: "R650", Rack: "B01", Zone: "ZoneB", Role: "server",
		Notes: []string{"Web应用节点"}},
	{Name: "db-01", Model: "R750", Rack: "B02", Zone: "ZoneB", Role: "server",
		Notes: []string{"MySQL数据库节点"}},
	{Name: "storage-01", Model: "CS8000", Rack: "C01", Zone: "ZoneC", Role: "storage",
		Notes: []string{"分布式存储节点"}},
	{Name: "switch-core-01", Model: "CE16800", Rack: "C02", Zone: "ZoneC", Role: "switch",
		Notes: []string{"核心交换机"}},
}

func Devices() []model.DeviceInfo { return devices }

func Neighbors(name string) []model.DeviceInfo {
	if name == "switch-core-01" {
		var out []model.DeviceInfo
		for _, d := range devices {
			if d.Name != name {
				out = append(out, d)
			}
		}
		return out
	}
	for _, d := range devices {
		if d.Name == name {
			return []model.DeviceInfo{devices[5]}
		}
	}
	return nil
}
