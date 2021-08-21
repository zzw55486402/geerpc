package geerpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"geerpc/codec"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
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
	MagicNumber    int           // 保证这是一个geerpc的请求
	CodecType      codec.Type    // 客户端选择不同的编解码的协议
	ConnectTimout  time.Duration // 超时时间
	HandlerTimeout time.Duration // 处理请求超时时间
}

var DefaultOption = &Option{
	MagicNumber:   MagicNumber,
	CodecType:     codec.GobType,
	ConnectTimout: time.Second * 10,
}

/*
	默认启动服务的方式
	lis, _ := net.Listen("tcp", ":9999")
	geerpc.Accept(lis)
*/
type Server struct {
	serviceMap sync.Map
}

func NewServer() *Server {
	return &Server{}
}

// Register方法
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

func Register(rcvr interface{}) error { return DefaultServer.Register(rcvr) }

func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".") // 分割为两部分
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
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
		go server.handleRequest(cc, req, sending, wg, time.Second)
	}
	wg.Wait()
	_ = cc.Close()
}

type request struct {
	h            *codec.Header // 请求头
	argv, replyv reflect.Value // 参数args 和 回复
	mtype        *methodType
	svc          *service
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
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()

	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
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

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	called := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		called <- struct{}{}
		if err != nil {
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			sent <- struct{}{}
			return
		}
		server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
		sent <- struct{}{}
	}()
	if timeout == 0 {
		<-called
		<-sent
		return
	}
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout: expect within %s", timeout)
		server.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called:
		<-sent
	}
}

const (
	connected        = "200 Connected to Gee RPC"
	defaultRPCPath   = "/_geerc_"
	defaultDebugPath = "/debug/geerpc"
)

// ServerHTTP handler
func (server *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "405 must CONNECT\n")
		return
	}
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Print("rpc hijacking", req.RemoteAddr, ":", err.Error())
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.0 "+connected+"\n\n")
	server.ServerConn(conn)
}

func (server *Server) HandleHTTP() {
	http.Handle(defaultRPCPath, server)
	http.Handle(defaultDebugPath, debugHTTP{server})
	log.Println("rpc server debug path:", defaultDebugPath)
}

func HandleHTPP() {
	DefaultServer.HandleHTTP()
}
