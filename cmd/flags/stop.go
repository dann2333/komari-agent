package flags_pkg

import "sync"

// 服务控制管理器要求停止时关闭。循环里收到它就返回，
// 好让服务先报告已停止，而不是直接把进程杀掉。
var (
	stopOnce sync.Once
	stopCh   = make(chan struct{})
)

func RequestStop() {
	stopOnce.Do(func() { close(stopCh) })
}

func Stopped() <-chan struct{} { return stopCh }
