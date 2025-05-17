package sequence

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
	"time"
)

var (
	sequence  uint32
	lastTime  uint64
	machineID [3]byte // 前3字节作为机器标识
)

func init() {
	// 初始化时读取机器标识
	if _, err := rand.Read(machineID[:]); err != nil {
		// 如果随机失败，使用时间戳作为机器标识
		ts := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(machineID[:], uint64(ts))
	}
}

func GenerateRequestID() uint64 {
	now := uint64(time.Now().UnixNano() / 1e6) // 毫秒

	seq := atomic.AddUint32(&sequence, 1)
	atomic.StoreUint64(&lastTime, now)

	var id [8]byte

	// 时间戳部分 (4字节)
	binary.BigEndian.PutUint32(id[0:4], uint32(now))

	// 机器标识部分 (3字节)
	copy(id[4:7], machineID[:])

	// 序列号部分 (1字节)
	id[7] = byte(seq)

	return binary.BigEndian.Uint64(id[:])
}

var requestIDCounter uint64

func GenerateRequestID_danji() uint64 {
	return atomic.AddUint64(&requestIDCounter, 1)
}
