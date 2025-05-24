package protocol

// future.go

import (
	"context"
	"time"
)

type Future struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan *Message
}

func NewFuture(timeout time.Duration) *Future {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return &Future{
		ctx:    ctx,
		cancel: cancel,
		ch:     make(chan *Message, 1),
	}
}

func (f *Future) Done(msg *Message) {
	select {
	case f.ch <- msg:
	default:
		// 可以加日志或 metrics
	}
	f.cancel()
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
	f.cancel()
}
