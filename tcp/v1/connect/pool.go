package connect

import (
	"net"
	"sync"
	"time"

	"github.com/alynlin/example/tcp/v1/protocol"
)

const (
	defaultReadTimeout   = 120 * time.Second
	defaultWriteTimeout  = 30 * time.Second
	defaultMaxBufferSize = 64 * 1024 // 64KB
	defaultDialTimeout   = 5 * time.Second
)

type BufferLimitCallback func(addr string, direction string, size int)

type ConnectionPool struct {
	mu          sync.Mutex
	connections sync.Map // key为"host:port"
	dialTimeout time.Duration

	maxBufferSize    int
	onBufferLimitHit BufferLimitCallback
	readTimeout      time.Duration
}

func NewConnectionPool(dialTimeout time.Duration, maxBufSize int, onBufLimit BufferLimitCallback) *ConnectionPool {
	return &ConnectionPool{
		dialTimeout:      dialTimeout,
		maxBufferSize:    maxBufSize,
		onBufferLimitHit: onBufLimit,
		readTimeout:      defaultReadTimeout,
	}
}

func (p *ConnectionPool) Get(addr string) (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if value, ok := p.connections.Load(addr); ok {
		conn := value.(*Connection)
		if conn.IsClosed() {
			p.connections.Delete(addr) // 主动移除关闭的连接
		} else {
			return conn, nil
		}
	}

	conn, err := p.newConnection(addr)
	if err != nil {
		return nil, err
	}

	p.connections.Store(addr, conn)
	return conn, nil
}

func (p *ConnectionPool) newConnection(addr string) (*Connection, error) {
	conn, err := net.DialTimeout("tcp", addr, p.dialTimeout)
	if err != nil {
		return nil, err
	}

	c := &Connection{
		conn:        conn,
		closeCh:     make(chan struct{}),
		writerCh:    make(chan *protocol.Message, 100),
		readBuf:     make([]byte, 4096),
		writeBuf:    make([]byte, 4096),
		maxBufSize:  p.maxBufferSize,
		addr:        addr,
		onLimit:     p.onBufferLimitHit,
		readTimeout: p.readTimeout,
	}

	go c.readLoop()
	go c.writeLoop()
	go c.startHeartbeat(30 * time.Second)
	go c.startReadTimeoutWatcher(c.readTimeout)

	return c, nil
}
