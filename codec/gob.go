package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

/*
	定义了一个GobCodec的结构体，这个结构体由四部分构成，conn由构建函数传入，通常是通过TCP或者Unix建立的socket时得到的链接的实例
	dec和enc分别是gob的反序列化和序列化，buf是为了防止阻塞而创建的带缓冲的Writer，为了提升性能。
*/
type GobCodec struct {
	conn io.ReadWriteCloser
	buf  *bufio.Writer
	dec  *gob.Decoder
	enc  *gob.Encoder
}

var _ Codec = (*GobCodec)(nil)

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}

// 实现Codec接口
// 读取headr
func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h)
}

// 读取body
func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}

// 编码header和body
func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		_ = c.buf.Flush()
		if err != nil {
			_ = c.Close()
		}
	}()
	if err := c.enc.Encode(h); err != nil {
		log.Println("rpc codec: gob error encoding header:", err)
		return err
	}
	if err := c.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	return nil
}

// 关闭链接
func (c *GobCodec) Close() error {
	return c.conn.Close()
}
