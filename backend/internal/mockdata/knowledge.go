package mockdata

var runbooks = map[string][]string{
	"cpu_usage": {
		"检查CPU负载曲线与top命令热点进程",
		"定位占用高的进程(如训练任务/挖矿进程)",
		"检查邻居节点是否过载导致负载不均衡",
		"视情况迁移负载或扩容",
	},
	"disk_usage": {
		"执行 df -h 确认挂载点使用率",
		"检查大文件与日志轮转是否失效",
		"清理冗余数据或扩容磁盘",
		"检查是否有进程异常写入撑满磁盘",
	},
	"gpu_util": {
		"检查 NVRM Xid 错误日志与 GPU 温度",
		"检查 NVLink 链路状态",
		"尝试重置 GPU 驱动或重启节点",
		"若持续掉卡则报修硬件",
	},
	"mem_usage": {
		"检查 dmesg OOM killer 日志",
		"查看内存占用Top进程",
		"调优内核内存参数或扩容内存",
		"检查是否有内存泄漏",
	},
	"net_in": {
		"检查交换机端口 down 日志与光模块告警",
		"检查链路对端设备状态",
		"联系网络侧排查链路质量",
		"检查是否因广播风暴导致拥塞",
	},
}

func Runbooks() map[string][]string { return runbooks }

func SearchRunbook(metric string) ([]string, bool) {
	steps, ok := runbooks[metric]
	return steps, ok
}
