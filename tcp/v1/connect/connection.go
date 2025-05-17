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

var bufPool = sync.Pool{
	New: func() interface{} {
		// Allocate a large enough buffer for most requests
		return make([]byte, 4096)
	},
}

type Connection struct {
	conn            net.Conn
	mu              sync.Mutex
	pendingRequests map[uint64]chan *v1.Message
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
			header := bufPool.Get().([]byte)[:v1.HeaderSize]
			if _, err := io.ReadFull(c.conn, header); err != nil {
				bufPool.Put(header)
				return
			}

			length := binary.BigEndian.Uint32(header[:4])
			reqID := binary.BigEndian.Uint64(header[4:12])
			msgType := header[12]
			bufPool.Put(header)

			body := bufPool.Get().([]byte)[:length-v1.HeaderSize]
			if _, err := io.ReadFull(c.conn, body); err != nil {
				bufPool.Put(body)
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
				// optional: handle heartbeat
			default:
				// unknown message type
			}
		}
	}
}

func (c *Connection) writeLoop() {
	defer c.Close()

	for {
		select {
		case msg := <-c.writerCh:
			buf := bufPool.Get().([]byte)
			size := v1.HeaderSize + len(msg.Body)
			if cap(buf) < size {
				buf = make([]byte, size)
			}
			buf = buf[:size]

			binary.BigEndian.PutUint32(buf[:4], msg.Length)
			binary.BigEndian.PutUint64(buf[4:12], msg.RequestID)
			buf[12] = msg.Type
			copy(buf[v1.HeaderSize:], msg.Body)

			if _, err := c.conn.Write(buf); err != nil {
				bufPool.Put(buf)
				return
			}
			bufPool.Put(buf)
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
