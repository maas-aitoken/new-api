package service

import (
	"sync/atomic"

	"github.com/QuantumNous/new-api/model"
)

// capabilityGateEvents 是进程唯一的门控事件 writer。
// 没有启动 writer 的入口（命令行子命令、单元测试）产生的事件会被直接丢弃。
var capabilityGateEvents atomic.Pointer[eventWriter[model.CapabilityGateEvent]]

// StartCapabilityGateEventWriter 启动事件 writer，重复调用无副作用。
// 队列、批量和刷新间隔由 CAPABILITY_GATE_EVENT_* 环境变量调整，见 startEventWriter。
func StartCapabilityGateEventWriter() {
	startEventWriter(&capabilityGateEvents, "capability gate event", "CAPABILITY_GATE_EVENT", model.BatchInsertCapabilityGateEvents)
}

// StopCapabilityGateEventWriter 停止 writer 并把队列里剩余的事件写完。
func StopCapabilityGateEventWriter() {
	stopEventWriter(&capabilityGateEvents)
}
