+---------+-----------+---------+-----------------+
| Length  | RequestID |  Type   |      Body       |
| (4字节) |  (8字节)  | (1字节) |   (变长)        |
+---------+-----------+---------+-----------------+

Length: 整个消息的长度(不包括Length自身)
RequestID: 唯一请求标识，用于匹配请求和响应
Type: 消息类型(如0x01请求, 0x02响应, 0x03心跳)
Body: 实际的有效载荷，可以是JSON、Protobuf等序列化格式



``` golang
pool := NewMultiConnPool("127.0.0.1:8080", 10, 5)
// 在需要时获取连接
conn, err := pool.Get()
if err != nil {
    // 处理错误
    return
}
defer pool.Put(conn)

// 使用连接发送请求
_, err = conn.Write(requestData)
if err != nil {
    // 连接可能已损坏，关闭它
    conn.Close()
    return
}
```