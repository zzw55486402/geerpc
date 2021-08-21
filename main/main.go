package main

import (
	"context"
	"geerpc"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type Foo struct{}
type Args struct{ Num1, Num2 int }

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func startServer(addrCh chan string) {
	var foo Foo
	l, _ := net.Listen("tcp", ":9999")
	_ = geerpc.Register(&foo)
	geerpc.HandleHTPP()
	addrCh <- l.Addr().String()
	_ = http.Serve(l, nil)
}

func call(addrCh chan string) {
	client, _ := geerpc.DialHTTP("tcp", <-addrCh)
	defer func() {
		_ = client.Close()
	}()
	time.Sleep(time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := &Args{Num1: i, Num2: i * i}
			var reply int
			if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}

func main() {
	log.SetFlags(0)
	ch := make(chan string)
	go call(ch)
	startServer(ch)
	// addr := make(chan string)
	// go startServer(addr)
	// client, _ := geerpc.Dial("tcp", <-addr) // 这里用的是默认的gob协议
	// defer func() {
	// 	_ = client.Close()
	// }()
	// time.Sleep(time.Second)
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

	// var wg sync.WaitGroup
	// for i := 0; i < 5; i++ {
	// 	wg.Add(1)
	// 	go func(i int) {
	// 		defer wg.Done()
	// 		args := fmt.Sprintf("geerpc req %d", i)
	// 		var reply string
	// 		if err := client.Call("Foo.Sum", args, &reply); err != nil {
	// 			log.Fatal("call Foo.Sum error:", err)
	// 		}
	// 		log.Println("reply", reply)
	// 	}(i)
	// }
	// wg.Wait()

	// typ := reflect.TypeOf(&wg)
	// for i := 0; i < typ.NumMethod(); i++ {
	// 	method := typ.Method(i)
	// 	argv := make([]string, 0, method.Type.NumIn())
	// 	returns := make([]string, 0, method.Type.NumOut())

	// 	for j := 1; j < method.Type.NumIn(); j++ {
	// 		argv = append(argv, method.Type.In(j).Name())
	// 	}
	// 	for j := 0; j < method.Type.NumOut(); j++ {
	// 		returns = append(returns, method.Type.Out(j).Name())
	// 	}
	// 	log.Printf("func (w *%s) %s(%s) %s",
	// 		typ.Elem().Name(),
	// 		method.Name,
	// 		strings.Join(argv, ","),
	// 		strings.Join(returns, ","),
	// 	)
	// }
	// ctx, _ := context.WithTimeout(context.Background(), time.Second)
	// var wg sync.WaitGroup
	// for i := 0; i < 5; i++ {
	// 	wg.Add(1)
	// 	go func(i int) {
	// 		defer wg.Done()
	// 		args := &Args{Num1: i, Num2: i * i}
	// 		var reply int
	// 		if err := client.Call(ctx, "Foo.Sum", args, &reply); err != nil {
	// 			log.Fatal("call Foo.Sum error:", err)
	// 		}
	// 		log.Println("%d + %d = %d", args.Num1, args.Num2, reply)
	// 	}(i)
	// }
	// wg.Wait()
}
