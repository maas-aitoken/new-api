package service

import (
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const eventWriterStopTimeout = 10 * time.Second

// eventWriter 把遥测事件批量写进日志库。
//
// 投递永远不阻塞请求：队列满了就丢弃并计数，遥测积压不能变成路由反压。
// 一次批量写入失败只记日志，不重试，也不影响后续批次。
type eventWriter[T any] struct {
	name          string
	queue         chan *T
	batchSize     int
	flushInterval time.Duration
	insert        func([]*T) error

	dropped atomic.Int64
	stop    chan struct{}
	done    chan struct{}
}

func newEventWriter[T any](name string, queueSize, batchSize int, flushInterval time.Duration, insert func([]*T) error) *eventWriter[T] {
	writer := &eventWriter[T]{
		name:          name,
		queue:         make(chan *T, queueSize),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		insert:        insert,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go writer.run()
	return writer
}

// startEventWriter 在 slot 里装入一个事件 writer，已经装过则什么也不做。
//
// 三个参数由 <envPrefix>_BUFFER_SIZE、_BATCH_SIZE、_FLUSH_MS 三个环境变量调整，
// 写法与 pkg/attemptlog/config.go 一致，超出范围的值会被截断：
//   - _BUFFER_SIZE  队列容量，默认 4096，范围 1-1000000
//   - _BATCH_SIZE   每批写入条数，默认 200，范围 1-10000，且不超过队列容量
//   - _FLUSH_MS     批量写入的最长等待毫秒数，默认 2000，范围 100-3600000
func startEventWriter[T any](slot *atomic.Pointer[eventWriter[T]], name, envPrefix string, insert func([]*T) error) {
	bufferSize := min(max(common.GetEnvOrDefault(envPrefix+"_BUFFER_SIZE", 4096), 1), 1000000)
	batchSize := min(max(common.GetEnvOrDefault(envPrefix+"_BATCH_SIZE", 200), 1), 10000, bufferSize)
	flushMillis := min(max(common.GetEnvOrDefault(envPrefix+"_FLUSH_MS", 2000), 100), 3600000)
	writer := newEventWriter(name, bufferSize, batchSize, time.Duration(flushMillis)*time.Millisecond, insert)
	if !slot.CompareAndSwap(nil, writer) {
		writer.shutdown(eventWriterStopTimeout)
	}
}

// stopEventWriter 取出 slot 里的 writer 并把队列里剩余的事件写完。
// 停止之后到达的事件会被丢弃，进程退出前调用即可。
func stopEventWriter[T any](slot *atomic.Pointer[eventWriter[T]]) {
	if writer := slot.Swap(nil); writer != nil {
		writer.shutdown(eventWriterStopTimeout)
	}
}

func (w *eventWriter[T]) enqueue(event *T) {
	select {
	case w.queue <- event:
	default:
		if n := w.dropped.Add(1); n == 1 || n%1000 == 0 {
			common.SysError(common.LogText("%s queue is full, dropped %d events so far", w.name, n))
		}
	}
}

func (w *eventWriter[T]) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	batch := make([]*T, 0, w.batchSize)
	for {
		select {
		case event := <-w.queue:
			batch = append(batch, event)
			if len(batch) < w.batchSize {
				continue
			}
		case <-ticker.C:
		case <-w.stop:
			for len(w.queue) > 0 {
				batch = append(batch, <-w.queue)
			}
			w.flush(batch)
			return
		}
		w.flush(batch)
		batch = batch[:0]
	}
}

func (w *eventWriter[T]) flush(batch []*T) {
	if len(batch) == 0 {
		return
	}
	// 遥测写入的任何失败（包括 panic）都不能带倒网关进程。
	defer func() {
		if r := recover(); r != nil {
			common.SysError(common.LogText("%s writer panicked: %v", w.name, r))
		}
	}()
	if err := w.insert(batch); err != nil {
		common.SysError(common.LogText("failed to write %d events to %s: %v", len(batch), w.name, err))
	}
}

// shutdown 通知 run 把剩余事件写完后退出，最多等待 timeout，防止数据库挂死拖住进程退出。
func (w *eventWriter[T]) shutdown(timeout time.Duration) {
	close(w.stop)
	select {
	case <-w.done:
	case <-time.After(timeout):
		common.SysError(common.LogText("timed out waiting for the %s writer to drain", w.name))
	}
}
