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

	v1 "github.com/alynlin/example/tcp/v1/message"
	"github.com/alynlin/example/tcp/v1/sequence"
)

type Connection struct {
	conn            net.Conn
	mu              sync.Mutex
	pendingRequests map[uint64]chan *v1.Message
	closeCh         chan struct{}
	writerCh        chan *v1.Message
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
	respCh := make(chan *v1.Message, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("connection closed")
	}
	c.pendingRequests[reqID] = respCh
	c.mu.Unlock()

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

	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, errors.New("response channel closed")
		}
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
			// 保证 readBuf 至少能容纳 Header
			if len(c.readBuf) < v1.HeaderSize {
				c.readBuf = make([]byte, v1.HeaderSize)
			}
			header := c.readBuf[:v1.HeaderSize]

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
			bodyLen := int(length) - v1.HeaderSize
			if cap(c.readBuf) < v1.HeaderSize+bodyLen {
				c.readBuf = make([]byte, v1.HeaderSize+bodyLen)
			}
			body := c.readBuf[v1.HeaderSize : v1.HeaderSize+bodyLen]

			if _, err := io.ReadFull(c.conn, body); err != nil {
				return
			}

			msg := &v1.Message{
				Length:    length,
				RequestID: reqID,
				Type:      msgType,
				Body:      append([]byte{}, body...), // 深拷贝避免后续覆盖
			}

			switch msgType {
			case v1.MessageTypeResponse:
				c.mu.Lock()
				ch, ok := c.pendingRequests[reqID]
				if ok {
					select {
					case ch <- msg:
					default:
					}
					delete(c.pendingRequests, reqID)
				}
				c.mu.Unlock()

			case v1.MessageTypeHeartbeat:
				// 可选 log: log.Printf("recv heartbeat from %s", c.addr)
				// ✅ 更新最后读取时间 or  任何合法包都更新
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
			size := v1.HeaderSize + len(msg.Body)
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
	_ = c.conn.Close()

	for reqID, ch := range c.pendingRequests {
		close(ch)
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
				RequestID: 0,
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
