package codec

import "io"

// 方法头
type Header struct {
	ServiceMethod string // 格式：服务名.方法名 通常与结构体和方法相映射
	Seq           uint64 // 请求的序号，用以区分不同的请求
	Error         string // Error是错误信息，客户端为空，如果服务端发生错误则将错误信息置于Error中
}

// Codec 对消息体进行编码解码的接口Codec
type Codec interface {
	io.Closer
	ReadHeader(*Header) error
	ReadBody(interface{}) error
	Write(*Header, interface{}) error
}

type NewCodecFunc func(io.ReadWriteCloser) Codec

type Type string

const (
	GobType  Type = "application/gob" // 编码和解码的协议（序列化与反序列化）
	JsonType Type = "application/json"
)

var NewCodecFuncMap map[Type]NewCodecFunc // 不同的协议对应不同的处理的函数
// 初始化函数
func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc, 0)
	NewCodecFuncMap[GobType] = NewGobCodec
}
