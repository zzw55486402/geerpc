package xclient

import (
	"io"
	"sync"
)

type XClient struct {
	d       Discovery
	mode    SelectMode
	opt     *Option
	mu      sync.Mutex
	clients map[string]*Client
}


var _ io.Closer = (*XClient)(nil)

func NewXClient(d Discovery, mode SelectMode, opt *Option) *XClient {
	return &XClient{d:d, mode: mode, opt: opt, clients: make(map[string]*Client}
}

func (xc *XClient) Close() error {
	xc.mu.Lock()
	defer xc.mu.Unlock()
	for key, client := range xc.clients{
		_ = client.Close()
		delete(xc.clients, key)
	}
	return nil
}

func (xc *XClient) dial(rpcAddr string) (*Client, error)  {
	
}