package connect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/alynlin/example/tcp/v1/protocol"
	"github.com/alynlin/example/tcp/v1/sequence"
)

type Connection struct {
	conn            net.Conn
	mu              sync.Mutex
	pendingRequests map[uint64]*protocol.Future
	closeCh         chan struct{}
	writerCh        chan *protocol.Message
	closed          bool

	readBuf  []byte
	writeBuf []byte

	maxBufSize int    // 最大缓冲区大小，header + body
	addr       string // "host:port"
	onLimit    BufferLimitCallback

	//
	lastReadTime time.Time     // 最后一次成功接收消息的时间
	readTimeout  time.Duration // 最大允许的空闲时间
}

func (c *Connection) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Connection) SendRequest(body []byte, timeout time.Duration) ([]byte, error) {
	reqID := sequence.GenerateRequestID()
	future := protocol.NewFuture(timeout)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("connection closed")
	}
	c.pendingRequests[reqID] = future
	c.mu.Unlock()

	msg := &protocol.Message{
		RequestID: reqID,
		Type:      protocol.MessageTypeRequest,
		Body:      body,
	}
	msg.Length = uint32(protocol.HeaderSize + len(msg.Body))

	select {
	case c.writerCh <- msg:
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pendingRequests, reqID)
		c.mu.Unlock()
		return nil, errors.New("send timeout")
	case <-c.closeCh:
		return nil, errors.New("connection closed")
	}

	// 等待响应
	resp, err := future.Await()

	c.mu.Lock()
	delete(c.pendingRequests, reqID)
	c.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Connection) readLoop() {
	defer c.Close()

	for {
		select {
		case <-c.closeCh:
			return
		default:
			// 保证 readBuf 至少能容纳 Header
			if len(c.readBuf) < protocol.HeaderSize {
				c.readBuf = make([]byte, protocol.HeaderSize)
			}
			header := c.readBuf[:protocol.HeaderSize]

			if _, err := io.ReadFull(c.conn, header); err != nil {
				return
			}

			length := binary.BigEndian.Uint32(header[:4])
			reqID := binary.BigEndian.Uint64(header[4:12])
			msgType := header[12]
			// 检查消息长度是否超出限制
			if length < 0 || length > uint32(c.maxBufSize) {
				if c.onLimit != nil {
					c.onLimit(c.addr, "read", int(length))
				}
				return
			}

			// 确保缓冲区容量足够
			bodyLen := int(length) - protocol.HeaderSize
			if cap(c.readBuf) < protocol.HeaderSize+bodyLen {
				c.readBuf = make([]byte, protocol.HeaderSize+bodyLen)
			}
			body := c.readBuf[protocol.HeaderSize : protocol.HeaderSize+bodyLen]

			if _, err := io.ReadFull(c.conn, body); err != nil {
				return
			}

			msg := &protocol.Message{
				Length:    length,
				RequestID: reqID,
				Type:      msgType,
				Body:      append([]byte{}, body...), // 深拷贝避免后续覆盖
			}

			switch msgType {
			case protocol.MessageTypeResponse:
				c.completeRequest(reqID, msg)
			case protocol.MessageTypeHeartbeat:
				// 可选 log: log.Printf("recv heartbeat from %s", c.addr)
				// 更新最后读取时间 or  任何合法包都更新
				log.Printf("recv heartbeat from %s", c.addr)
				c.updateLastReadTime()

			default:
				// 忽略未知包，或关闭连接
			}
		}
	}
}

func (c *Connection) writeLoop() {
	defer c.Close()

	for {
		select {
		case msg := <-c.writerCh:
			size := protocol.HeaderSize + len(msg.Body)
			if size > c.maxBufSize {
				if c.onLimit != nil {
					c.onLimit(c.addr, "write", size)
				}
				continue
			}
			if cap(c.writeBuf) < size {
				c.writeBuf = make([]byte, size)
			}
			buf := c.writeBuf[:size]

			binary.BigEndian.PutUint32(buf[:4], msg.Length)
			binary.BigEndian.PutUint64(buf[4:12], msg.RequestID)
			buf[12] = msg.Type
			copy(buf[protocol.HeaderSize:], msg.Body)

			if _, err := c.conn.Write(buf); err != nil {
				return
			}
		case <-c.closeCh:
			return
		}
	}
}

func (c *Connection) completeRequest(reqID uint64, msg *protocol.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if future, ok := c.pendingRequests[reqID]; ok {
		future.Done(msg)
		delete(c.pendingRequests, reqID)
		future = nil // 释放内存，避免内存泄漏
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
	_ = c.conn.Close()

	for reqID, future := range c.pendingRequests {
		future.Cancel()
		delete(c.pendingRequests, reqID)
	}
}

func (c *Connection) startHeartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg := &protocol.Message{
				RequestID: 0,
				Type:      protocol.MessageTypeHeartbeat,
				Body:      nil,
			}
			msg.Length = uint32(protocol.HeaderSize)

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

func (c *Connection) startReadTimeoutWatcher(timeout time.Duration) {
	ticker := time.NewTicker(timeout / 2) // 比timeout更频繁地检查
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			idle := time.Since(c.lastReadTime)

			if idle > timeout {
				// 长时间未收到任何消息，主动关闭连接
				fmt.Printf("Connection %s idle for too long, closing", c.addr)
				c.Close()
				return
			}
		case <-c.closeCh:
			return
		}
	}
}

func (c *Connection) updateLastReadTime() {
	// 如果你希望更严谨可加锁或使用 atomic.Value，但业务上直接赋值是可以接受的
	c.lastReadTime = time.Now()
}
