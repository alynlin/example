package v1

import (
	"encoding/json"
	"time"

	"github.com/alynlin/example/tcp/v1/connect"
)

type RPCClient struct {
	pool *connect.ConnectionPool
}

func NewRPCClient() *RPCClient {
	return &RPCClient{
		pool: connect.NewConnectionPool(5 * time.Second),
	}
}

func (c *RPCClient) Call(addr string, method string, params interface{}, timeout time.Duration) ([]byte, error) {
	conn, err := c.pool.Get(addr)
	if err != nil {
		return nil, err
	}

	request := map[string]interface{}{
		"method":    method,
		"params":    params,
		"timestamp": time.Now().Unix(),
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	return conn.SendRequest(body, timeout)
}
