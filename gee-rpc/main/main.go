package main

import (
	gee_rpc "LearnGo/gee-rpc"
	"context"
	"log"
	"net"
	"sync"
	"time"
)

type Foo int

type Args struct {
	Num1 int
	Num2 int
}

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func main() {
	//log.SetFlags(0)
	addr := make(chan string)
	go startServer(addr) // 启动服务器

	client, _ := gee_rpc.Dial("tcp", <-addr)
	defer func() {
		_ = client.Close()
	}()

	time.Sleep(time.Second) //睡1秒，给服务器时间

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := &Args{Num1: i, Num2: i * i}
			var reply int
			ctx, _ := context.WithTimeout(context.Background(), time.Second)
			if err := client.Call(ctx, "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error", err)
			}
			log.Printf("%d + %d = %d: ", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}

// 开启服务端
func startServer(addr chan string) {
	var foo Foo
	if err := gee_rpc.Register(&foo); err != nil {
		log.Fatal("register error: ", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:7000")
	if err != nil {
		log.Fatal("network error: ", err)
	}
	log.Println("start rpc server on", l.Addr())
	addr <- l.Addr().String()
	gee_rpc.Accept(l)
}
