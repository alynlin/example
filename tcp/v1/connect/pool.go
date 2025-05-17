package connect

import (
	"net"
	"sync"
	"time"

	v1 "github.com/alynlin/example/tcp/v1/message"
)

type ConnectionPool struct {
	mu          sync.Mutex
	connections map[string]*Connection // key为"host:port"
	dialTimeout time.Duration
}

func NewConnectionPool(dialTimeout time.Duration) *ConnectionPool {
	return &ConnectionPool{
		connections: make(map[string]*Connection),
		dialTimeout: dialTimeout,
	}
}

func (p *ConnectionPool) Get(addr string) (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if conn, ok := p.connections[addr]; ok && !conn.IsClosed() {
		return conn, nil
	}

	conn, err := p.newConnection(addr)
	if err != nil {
		return nil, err
	}

	p.connections[addr] = conn
	return conn, nil
}

func (p *ConnectionPool) newConnection(addr string) (*Connection, error) {
	conn, err := net.DialTimeout("tcp", addr, p.dialTimeout)
	if err != nil {
		return nil, err
	}

	c := &Connection{
		conn:            conn,
		pendingRequests: make(map[uint64]chan *v1.Message),
		closeCh:         make(chan struct{}),
		writerCh:        make(chan *v1.Message, 100),
	}

	go c.readLoop()
	go c.writeLoop()

	return c, nil
}
