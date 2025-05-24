package protocol

// 协议帧结构
type Message struct {
	Length    uint32 // 不包括Length自身的长度
	RequestID uint64 // 唯一请求ID
	Type      uint8  // 消息类型
	Body      []byte // 消息体
}

const (
	MessageTypeRequest  = 0x01
	MessageTypeResponse = 0x02
	// todo  是否改为 ping/pong 类型？
	MessageTypeHeartbeat = 0x03 // 心跳包类型
	HeaderSize           = 13   // 4+8+1
)
