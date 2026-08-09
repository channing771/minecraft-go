// Package network 定义端无关消息协议与传输接口。
package network

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ClientMessage 是客户端可发送消息的封闭集合。
type ClientMessage interface {
	clientMessage()
}

// ServerMessage 是服务端可发送消息的封闭集合。
type ServerMessage interface {
	serverMessage()
}

func finite32(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func finiteVec3(value mgl32.Vec3) bool {
	for _, component := range value {
		if !finite32(component) {
			return false
		}
	}
	return true
}
