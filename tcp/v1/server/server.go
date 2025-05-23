package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"

	"github.com/alynlin/example/tcp/v1/protocol"
)

type RPCServer struct {
	addr     string
	handler  func(method string, params []byte) ([]byte, error)
	listener net.Listener
}

func NewRPCServer(addr string, handler func(string, []byte) ([]byte, error)) *RPCServer {
	return &RPCServer{
		addr:    addr,
		handler: handler,
	}
}

func (s *RPCServer) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = listener

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}

		go s.handleConn(conn)
	}
}

func (s *RPCServer) Shutdown(ctx context.Context) error {

	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *RPCServer) handleConn(conn net.Conn) {
	defer conn.Close()

	for {
		// 读取消息头
		header := make([]byte, protocol.HeaderSize)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		length := binary.BigEndian.Uint32(header[:4])
		reqID := binary.BigEndian.Uint64(header[4:12])
		msgType := header[12]

		switch msgType {
		case protocol.MessageTypeRequest:
			s.handleRequest(conn, reqID, length)
			continue
		case protocol.MessageTypeHeartbeat:
			// 可选 log: log.Printf("recv heartbeat from %s", c.addr)
			log.Printf("server recv heartbeat from %s", conn.RemoteAddr())
			s.handleHeartbeat(conn)
			continue
		}

	}
}

func (s *RPCServer) handleHeartbeat(conn net.Conn) {
	msg := &protocol.Message{
		RequestID: 0,
		Type:      protocol.MessageTypeHeartbeat,
		Body:      nil,
	}
	msg.Length = uint32(protocol.HeaderSize)

	buf := make([]byte, msg.Length)
	binary.BigEndian.PutUint32(buf[:4], msg.Length)
	binary.BigEndian.PutUint64(buf[4:12], msg.RequestID)
	buf[12] = msg.Type
	copy(buf[protocol.HeaderSize:], msg.Body)
	_, _ = conn.Write(buf)
}
func (s *RPCServer) handleRequest(conn net.Conn, reqID uint64, length uint32) {

	// 读取消息体
	body := make([]byte, length-protocol.HeaderSize)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}

	// 处理请求

	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		result, _ := json.Marshal(map[string]interface{}{"error": "invalid json"})
		s.writeError(conn, reqID, result)
		return
	}

	method, _ := request["method"].(string)
	params, _ := json.Marshal(request["params"])

	// 调用处理器
	result, err := s.handler(method, params)
	if err != nil {
		result, _ = json.Marshal(map[string]interface{}{"error": err.Error()})
	}

	// 发送响应
	resp := &protocol.Message{
		Length:    uint32(protocol.HeaderSize + len(result)),
		RequestID: reqID,
		Type:      protocol.MessageTypeResponse,
		Body:      result,
	}

	buf := make([]byte, protocol.HeaderSize+len(resp.Body))
	binary.BigEndian.PutUint32(buf[:4], resp.Length)
	binary.BigEndian.PutUint64(buf[4:12], resp.RequestID)
	buf[12] = resp.Type
	copy(buf[protocol.HeaderSize:], resp.Body)

	if _, err := conn.Write(buf); err != nil {
		return
	}
}

func (s *RPCServer) writeError(conn net.Conn, reqID uint64, result []byte) {
	resp := &protocol.Message{
		Length:    uint32(protocol.HeaderSize + len(result)),
		RequestID: reqID,
		Type:      protocol.MessageTypeResponse,
		Body:      result,
	}
	buf := make([]byte, resp.Length)
	binary.BigEndian.PutUint32(buf[:4], resp.Length)
	binary.BigEndian.PutUint64(buf[4:12], resp.RequestID)
	buf[12] = resp.Type
	copy(buf[protocol.HeaderSize:], resp.Body)
	_, _ = conn.Write(buf)
}
