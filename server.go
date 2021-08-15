package geerpc

import (
	"encoding/json"
	"fmt"
	"geerpc/codec"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
)

// 协商消息的编解码方式 放到结构体Option中承载
// option通过json解码得到对应的编解码的协议CodecType 然后对剩下的header和body进行编解码
/*
	|Option{MagicNumber: xxx, CodecType: xxx}|Header{ServiceMethod ...}|Body interface{}|
		固定的json编解码							header和body的编解码方式由CodeType决定

	|Option|Header1|Body1|Header2|Body2|...
*/
const MagicNumber = 0x3bef5c

type Option struct {
	MagicNumber int        // 保证这是一个geerpc的请求
	CodecType   codec.Type // 客户端选择不同的编解码的协议
}

var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodecType:   codec.GobType,
}

/*
	默认启动服务的方式
	lis, _ := net.Listen("tcp", ":9999")
	geerpc.Accept(lis)
*/
type Server struct{}

func NewServer() *Server {
	return &Server{}
}

// 默认的Server实例
var DefaultServer = NewServer()

// 监听链接
func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept error:", err)
			return
		}
		// 监听链接 开启子协程来处理
		go server.ServerConn(conn)
	}
}
func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

// ServerConn用来处理前面说的通信过程 反序列化Option实例得到对应的消息编解码器
// 对传过来的conn的option部分通过json进行解码，验证是否是对应的geerpc和对应的header和body的编解码方式
func (server *Server) ServerConn(conn io.ReadWriteCloser) {
	defer func() {
		_ = conn.Close()
	}()
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", err)
		return
	}
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}
	server.ServerCodec(f(conn))
}

/*
	一次连接可以有多个请求，即多个header和body 使用for循环来无限制的等待请求的到来
	直到请求出错（conn被关闭或者接受到的报文有问题）
	使用handleRequest协程来并发执行请求
	处理的请求是并发的 但是回复请求的报文必须是逐个发送的，并发容易导致多个活肤报文交织在一起
	客户端无法解析，这里使用了锁sending来保证

	尽力而为，直到header解析失败，才终止循环。
*/
var invalidRequest = struct{}{}

func (server *Server) ServerCodec(cc codec.Codec) {
	sending := new(sync.Mutex) // 加锁 保证返回一个完整的response
	wg := new(sync.WaitGroup)  // 保证请求被完整的处理
	for {
		// 读取请求
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			// 回复请求
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		// 处理请求
		go server.handleRequest(cc, req, sending, wg)
	}
	wg.Wait()
	_ = cc.Close()
}

type request struct {
	h            *codec.Header // 请求头
	argv, replyv reflect.Value // 参数args 和 回复
}

// 读取请求头
func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	return &h, nil
}

// 读取请求并处理body
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	// 判断请求的参数的类型 采用反射
	req.argv = reflect.New(reflect.TypeOf(""))
	if err = cc.ReadBody(req.argv.Interface()); err != nil {
		log.Println("rpc server: read argv err:", err)
	}
	return req, nil
}

func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Println(req.h, req.argv.Elem())
	req.replyv = reflect.ValueOf(fmt.Sprintf("geerpc resp %d", req.h.Seq))
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}
