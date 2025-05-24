package protocol

// future.go

import (
	"context"
	"sync"
	"time"
)

type Future struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan *Message
	once   sync.Once
}

func NewFuture(ctx context.Context, timeout time.Duration) *Future {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return &Future{
		ctx:    ctx,
		cancel: cancel,
		ch:     make(chan *Message, 1),
	}
}

func NewFutureWithCtx(ctx context.Context) *Future {
	c, cancel := context.WithCancel(ctx)
	return &Future{
		ctx:    c,
		cancel: cancel,
		ch:     make(chan *Message, 1),
	}
}

func (f *Future) Done(msg *Message) {
	f.once.Do(func() {
		select {
		case f.ch <- msg:
		default:
			// 可以加日志或 metrics
		}
		f.cancel()
	})
}

func (f *Future) Await() (*Message, error) {
	select {
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	case msg := <-f.ch:
		return msg, nil
	}
}

func (f *Future) Cancel() {
	f.once.Do(func() {
		f.cancel()
	})
}

// 调试用
func (f *Future) Deadline() (time.Time, bool) {
	return f.ctx.Deadline()
}
