package mockdata

type Doc struct {
	ID          string
	Title       string
	DeviceTypes []string
	Metrics     []string
	Keywords    []string
	Body        string
}

var docs = []Doc{
	{ID: "doc-cpu-hot", Title: "CPU热点进程排查", DeviceTypes: []string{"gpu-server", "server"}, Metrics: []string{"cpu_usage"},
		Keywords: []string{"load", "scheduler", "热点进程"}, Body: "CPU使用率持续高于85%时，检查top命令热点进程，优先定位训练任务与异常进程，必要时迁移负载。"},
	{ID: "doc-disk-full", Title: "磁盘空间不足处理", DeviceTypes: []string{"gpu-server", "server", "storage"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"no space", "ext4", "磁盘"}, Body: "磁盘使用率超过95%时执行df -h定位挂载点，检查大文件与日志轮转，清理或扩容，排查异常写入进程。"},
	{ID: "doc-gpu-xid", Title: "GPU掉卡与Xid错误", DeviceTypes: []string{"gpu-server"}, Metrics: []string{"gpu_util"},
		Keywords: []string{"Xid", "NVLink", "nvidia-smi"}, Body: "GPU利用率归零时检查NVRM Xid日志与NVLink链路，尝试重置驱动或重启节点，持续掉卡报修硬件。"},
	{ID: "doc-gpu-temp", Title: "GPU温度过高", DeviceTypes: []string{"gpu-server"}, Metrics: []string{"gpu_util"},
		Keywords: []string{"温度", "thermal", "风扇"}, Body: "GPU温度超过90度时检查散热与风扇转速，清理尘堵，降低负载或加装散热。"},
	{ID: "doc-mem-oom", Title: "内存溢出与OOM", DeviceTypes: []string{"gpu-server", "server"}, Metrics: []string{"mem_usage"},
		Keywords: []string{"OOM", "OutOfMemory", "memory", "oom-kill"}, Body: "内存使用率超过90%时检查dmesg OOM日志与内存Top进程，定位内存泄漏，调优内核参数或扩容。"},
	{ID: "doc-net-link", Title: "网络链路中断排查", DeviceTypes: []string{"switch", "server"}, Metrics: []string{"net_in"},
		Keywords: []string{"link down", "transceiver", "光模块"}, Body: "入口流量中断时检查端口down日志与光模块告警，核对对端设备状态，必要时联系网络侧。"},
	{ID: "doc-load-balance", Title: "负载不均衡处理", DeviceTypes: []string{"gpu-server"}, Metrics: []string{"cpu_usage"},
		Keywords: []string{"负载均衡", "邻居节点", "调度"}, Body: "多节点CPU负载差异大时检查邻居节点是否过载导致不均衡，视情况迁移负载。"},
	{ID: "doc-storage-full", Title: "存储节点磁盘告警", DeviceTypes: []string{"storage"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"分布式存储", "数据均衡", "副本"}, Body: "存储节点磁盘告警时检查数据分布均衡度与副本数，扩容或迁移数据，避免单节点热点。"},
	{ID: "doc-db-tablespace", Title: "数据库表空间不足", DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"InnoDB", "tablespace", "mysql"}, Body: "数据库节点磁盘满时检查表空间与binlog占用，清理归档并评估扩容，避免InnoDB分配页失败。"},
	{ID: "doc-network-flap", Title: "端口抖动与广播风暴", DeviceTypes: []string{"switch"}, Metrics: []string{"net_in"},
		Keywords: []string{"flapping", "广播风暴", "拥塞"}, Body: "端口持续抖动或流量异常时检查广播风暴与STP收敛，定位环网并隔离异常端口。"},
}

func Docs() []Doc { return docs }
