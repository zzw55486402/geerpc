package geerpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"geerpc/codec"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Call struct {
	Seq           uint64
	ServiceMethod string      // format Service.Method
	Args          interface{} // arguments
	Reply         interface{} // reply
	Error         error       //  return error
	Done          chan *Call  // when call done notice to rpc Call
}

func (call *Call) done() {
	call.Done <- call
}

// 实现rpc的客户端 可以并发的goroutine请求 一个客户端执行rpc调用
type Client struct {
	cc       codec.Codec // 消息的编解码器
	opt      *Option
	sending  sync.Mutex   // 互斥锁 保证并发请求的有序发送
	header   codec.Header // 请求消息头 只需要一个附庸
	mu       sync.Mutex
	seq      uint64           // 请求唯一编号
	pending  map[uint64]*Call // 保存未处理完成的请求 键是seq序号 值是Call实例
	closing  bool             // users had called Close
	shutdown bool             // server has told us to stop
}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

// 关闭链接
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing {
		return ErrShutdown
	}
	client.closing = true
	return client.cc.Close()
}

// 是否可用
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	// 既没被shutdown，也没被关闭，任意有一个为true则不可用
	// closing是用户主动关闭的 shutdown是因为错误关闭的
	return !client.shutdown && !client.closing
}

// 注册Call 将call添加到client.pending 并且更新client.seq
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

// 删除Call并返回对应的call
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

// 终止Call 并将产生的错误通知所有的call
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

// 客户端接收响应  发送请求
// 存在三种情况：call 存在 但是请求没有发送完整 或者因为其他原因被取消了 但服务端仍旧处理了
// call存在 但是服务端处理出错， h.Error不为空
// call 存在 服务端处理正常 需要从body中都去Reply的值
func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		call := client.removeCall(h.Seq)
		switch {
		case call == nil:
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	client.terminateCalls(err)
}

// 创建client实例并完成协议的交换，获取对应消息的编解码的函数
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	// send options with server
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: option error:", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

// 创建client实例
func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, // seq starts with 1, 0 means invalid call
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	// 开启协程接受响应
	go client.receive()
	return client
}

// 得到opt参数 默认为gob协议
func parseOptions(opts ...*Option) (*Option, error) {
	// if opts is nil or pass nil as parameter
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

// 实现Dial函数 用于客户端传入服务端地址 创建Client实例，简化调用将Option转为可选参数
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	return dialTimeout(NewClient, network, address, opts...)
}

// 实现发送请求的能力
func (client *Client) send(call *Call) {
	// make sure that the client will send a complete request
	client.sending.Lock()
	defer client.sending.Unlock()

	// register this call.
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	// prepare request header
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	// encode and send the request
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq)
		// 如果call为nil 就直接的结束这个回调请求
		if call != nil {
			call.Error = err
			call.done()
		}
	}

}

// Call 和 Go 是暴露给用户的两个RPC服务调用接口，Go是一个异步接口，返回call实例。
// Call 是对 Go 的封装，阻塞call.Done, 等待响应返回，是一个同步接口。

func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

func (client *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	select {
	case <-ctx.Done():
		client.removeCall(call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done:
		return call.Error
	}
}

type clientResult struct {
	client *Client
	err    error
}

type newClientFunc func(conn net.Conn, opt *Option) (client *Client, err error)

func dialTimeout(f newClientFunc, network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout(network, address, opt.ConnectTimout)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	ch := make(chan clientResult)
	go func() {
		client, err := f(conn, opt)
		ch <- clientResult{client: client, err: err}
	}()
	if opt.ConnectTimout == 0 {
		result := <-ch
		return result.client, result.err
	}
	select {
	case <-time.After(opt.ConnectTimout):
		return nil, fmt.Errorf("rpc client: conneciont timeout: expect within %s", opt.ConnectTimout)
	case result := <-ch:
		return result.client, result.err
	}
}

func NewHTTPClient(conn net.Conn, opt *Option) (*Client, error) {
	_, _ = io.WriteString(conn, fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", defaultRPCPath))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{
		Method: "CONNECT",
	})
	if err == nil && resp.Status == connected {
		return NewClient(conn, opt)
	}
	if err == nil {
		err = errors.New("unexpected HTTP response: " + resp.Status)
	}
	return nil, err
}

func DialHTTP(network, address string, opts ...*Option) (*Client, error) {
	return dialTimeout(NewHTTPClient, network, address, opts...)
}

func XDial(rpcAddr string, opts ...*Option) (*Client, error) {
	parts := strings.Split(rpcAddr, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("rpc client err: wrong format '%s', expect protocol@addr", rpcAddr)
	}
	protocol, addr := parts[0], parts[1]
	switch protocol {
	case "http":
		return DialHTTP("tcp", addr, opts...)
	default:
		return Dial(protocol, addr, opts...)
	}
}
