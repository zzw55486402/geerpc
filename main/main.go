package main

import (
	"fmt"
	"geerpc"
	"log"
	"net"
	"sync"
	"time"
)

func startServer(addr chan string) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("strat rpc server on", l.Addr())
	addr <- l.Addr().String()
	geerpc.Accept(l)
}

func main() {
	addr := make(chan string)
	go startServer(addr)
	client, _ := geerpc.Dial("tcp", <-addr) // 这里用的是默认的gob协议
	defer func() {
		_ = client.Close()
	}()
	time.Sleep(time.Second)
	// send options
	// _ = json.NewEncoder(conn).Encode(geerpc.DefaultOption)
	// cc := codec.NewGobCodec(conn)
	// for i := 0; i < 5; i++ {
	// 	h := &codec.Header{
	// 		ServiceMethod: "Foo.Sum",
	// 		Seq:           uint64(i),
	// 	}
	// 	_ = cc.Write(h, fmt.Sprintf("geerpc req %d", h.Seq))
	// 	_ = cc.ReadHeader(h)
	// 	var reply string
	// 	_ = cc.ReadBody(&reply)
	// 	log.Println("reply:", reply)
	// }

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("geerpc req %d", i)
			var reply string
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Println("reply", reply)
		}(i)
	}
	wg.Wait()
}
