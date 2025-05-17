package connect

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	v1 "github.com/alynlin/example/tcp/v1/message"
	"github.com/alynlin/example/tcp/v1/sequence"
)

type Connection struct {
	conn            net.Conn
	mu              sync.Mutex
	pendingRequests map[uint64]chan *v1.Message // 请求ID到响应channel的映射
	closeCh         chan struct{}
	writerCh        chan *v1.Message
	closed          bool
}

func (c *Connection) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Connection) SendRequest(body []byte, timeout time.Duration) ([]byte, error) {
	reqID := sequence.GenerateRequestID()
	respCh := make(chan *v1.Message, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("connection closed")
	}
	c.pendingRequests[reqID] = respCh
	c.mu.Unlock()

	// 发送请求
	msg := &v1.Message{
		RequestID: reqID,
		Type:      v1.MessageTypeRequest,
		Body:      body,
	}
	msg.Length = uint32(v1.HeaderSize + len(msg.Body))

	select {
	case c.writerCh <- msg:
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pendingRequests, reqID)
		c.mu.Unlock()
		return nil, errors.New("send timeout")
	}

	// 等待响应
	select {
	case resp := <-respCh:
		return resp.Body, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pendingRequests, reqID)
		c.mu.Unlock()
		return nil, errors.New("request timeout")
	case <-c.closeCh:
		return nil, errors.New("connection closed")
	}
}

func (c *Connection) readLoop() {
	defer c.Close()

	for {
		select {
		case <-c.closeCh:
			return
		default:
			// 读取消息头
			header := make([]byte, v1.HeaderSize)
			if _, err := io.ReadFull(c.conn, header); err != nil {
				return
			}

			length := binary.BigEndian.Uint32(header[:4])
			reqID := binary.BigEndian.Uint64(header[4:12])
			msgType := header[12]

			// 读取消息体
			body := make([]byte, length-v1.HeaderSize)
			if _, err := io.ReadFull(c.conn, body); err != nil {
				return
			}

			msg := &v1.Message{
				Length:    length,
				RequestID: reqID,
				Type:      msgType,
				Body:      body,
			}

			switch msgType {
			case v1.MessageTypeResponse:
				c.mu.Lock()
				if ch, ok := c.pendingRequests[reqID]; ok {
					ch <- msg
					delete(c.pendingRequests, reqID)
				}
				c.mu.Unlock()
			case v1.MessageTypeHeartbeat:
				// 处理心跳
			}
		}
	}
}

func (c *Connection) writeLoop() {
	defer c.Close()

	for {
		select {
		case msg := <-c.writerCh:
			buf := make([]byte, v1.HeaderSize+len(msg.Body))
			binary.BigEndian.PutUint32(buf[:4], msg.Length)
			binary.BigEndian.PutUint64(buf[4:12], msg.RequestID)
			buf[12] = msg.Type
			copy(buf[v1.HeaderSize:], msg.Body)

			if _, err := c.conn.Write(buf); err != nil {
				return
			}
		case <-c.closeCh:
			return
		}
	}
}

func (c *Connection) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.closed = true
	close(c.closeCh)
	c.conn.Close()

	// 通知所有等待的请求
	for reqID, ch := range c.pendingRequests {
		close(ch)
		delete(c.pendingRequests, reqID)
	}
}

func (c *Connection) SendBatch(requests [][]byte, timeout time.Duration) ([][]byte, error) {
	batchID := sequence.GenerateRequestID()
	respCh := make(chan *v1.Message, len(requests))
	results := make([][]byte, len(requests))

	// 注册所有请求
	c.mu.Lock()
	for i := range requests {
		reqID := batchID + uint64(i)
		c.pendingRequests[reqID] = respCh
	}
	c.mu.Unlock()

	// 确保在退出时清理资源
	defer c.cleanupBatch(batchID, uint64(len(requests)))

	// 发送所有请求
	for i, body := range requests {
		msg := &v1.Message{
			RequestID: batchID + uint64(i),
			Type:      v1.MessageTypeRequest,
			Body:      body,
		}
		msg.Length = uint32(v1.HeaderSize + len(msg.Body))

		select {
		case c.writerCh <- msg:
		case <-time.After(timeout):
			return nil, errors.New("send timeout")
		}
	}

	// 收集响应
	deadline := time.Now().Add(timeout)
	for i := 0; i < len(requests); i++ {
		select {
		case resp := <-respCh:
			idx := int(resp.RequestID - batchID)
			if idx >= 0 && idx < len(results) {
				results[idx] = resp.Body
			}
		case <-time.After(time.Until(deadline)):
			return nil, errors.New("request timeout")
		case <-c.closeCh:
			return nil, errors.New("connection closed")
		}
	}

	// 取消defer的清理，因为所有请求都已完成
	c.mu.Lock()
	for i := uint64(0); i < uint64(len(requests)); i++ {
		delete(c.pendingRequests, batchID+i)
	}
	c.mu.Unlock()

	return results, nil
}

func (c *Connection) cleanupBatch(batchID uint64, count uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := uint64(0); i < count; i++ {
		reqID := batchID + i
		if ch, ok := c.pendingRequests[reqID]; ok {
			close(ch)
		}
		// Go 1.18+ 可以直接使用delete内置函数
		delete(c.pendingRequests, reqID)
	}
}

func (c *Connection) startHeartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg := &v1.Message{
				RequestID: 0, // 心跳使用特殊ID
				Type:      v1.MessageTypeHeartbeat,
				Body:      nil,
			}
			msg.Length = uint32(v1.HeaderSize)

			select {
			case c.writerCh <- msg:
			case <-c.closeCh:
				return
			}
		case <-c.closeCh:
			return
		}
	}
}
