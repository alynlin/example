package conncheck

import (
	"errors"
	"net"
	"runtime"
	"syscall"
	"time"
)

// IsAlive 判断 TCP 连接是否存活（跨平台，非侵入式）
// 支持 Linux/macOS 使用 syscall 方式进行 peek 检测，其他系统 fallback 到读取超时判断
func IsAlive(conn net.Conn) bool {
	if conn == nil {
		return false
	}

	switch runtime.GOOS {
	case "linux", "darwin":
		return peekAlive(conn)
	default:
		return timeoutAlive(conn)
	}
}

// Linux/macOS: 使用 syscall.Recvfrom + MSG_PEEK 判断连接状态
func peekAlive(conn net.Conn) bool {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return false
	}
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return false
	}

	alive := true
	_ = rawConn.Read(func(fd uintptr) bool {
		buf := make([]byte, 1)
		n, _, err := syscall.Recvfrom(int(fd), buf, syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				return true
			}
			alive = false
			return false
		}
		if n == 0 {
			alive = false
		}
		return true
	})

	return alive
}

// 其他平台 fallback 方法：设置读取超时尝试读1字节
func timeoutAlive(conn net.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{}) // 清除 deadline

	if err == nil {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}
