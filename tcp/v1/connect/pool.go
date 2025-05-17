package connect

import (
	"errors"
	"net"
	"sync"
	"syscall"
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

// =========
type MultiConnPool struct {
	addr      string
	dialFunc  func() (net.Conn, error)
	pool      chan net.Conn
	maxConns  int
	idleConns int
	mu        sync.Mutex
}

func NewMultiConnPool(addr string, maxConns, idleConns int) *MultiConnPool {
	return &MultiConnPool{
		addr:      addr,
		pool:      make(chan net.Conn, maxConns),
		maxConns:  maxConns,
		idleConns: idleConns,
		dialFunc:  func() (net.Conn, error) { return net.Dial("tcp", addr) },
	}
}

func (p *MultiConnPool) Get() (net.Conn, error) {
	select {
	case conn := <-p.pool:
		// 检查连接是否有效
		if !isConnectionAlive(conn) {
			conn.Close()
			return p.createNewConnection()
		}
		return conn, nil
	default:
		return p.createNewConnection()
	}
}

func (p *MultiConnPool) createNewConnection() (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.pool) >= p.maxConns {
		return nil, errors.New("connection pool exhausted")
	}

	conn, err := p.dialFunc()
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (p *MultiConnPool) Put(conn net.Conn) {
	if conn == nil {
		return
	}

	select {
	case p.pool <- conn:
		// 成功放回池中
	default:
		// 池已满，关闭多余连接
		conn.Close()
	}
}

// isConnectionAlive 判断 TCP 连接是否仍然活着（非侵入式，不会读取真实业务数据）
func isConnectionAlive(conn net.Conn) bool {
	if conn == nil {
		return false
	}

	// 设置一个非常短的读取超时
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))

	// 使用 syscall MSG_PEEK 尝试“偷窥”数据，不会真正读取它
	// 仅适用于 *net.TCPConn 类型
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return false
	}

	// 获取底层 fd
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return false
	}

	var alive = true

	err = rawConn.Read(func(fd uintptr) bool {
		// syscall.Recvfrom + MSG_PEEK + MSG_DONTWAIT 组合尝试非阻塞读取
		buf := make([]byte, 1)
		n, _, err := syscall.Recvfrom(int(fd), buf, syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				// 无数据但未报错，连接活着
				return true
			}
			alive = false
			return false
		}
		if n == 0 {
			// 收到 0 字节，一般表示对端已关闭
			alive = false
			return false
		}
		return true
	})

	_ = conn.SetReadDeadline(time.Time{}) // 清除 deadline

	return err == nil && alive
}
