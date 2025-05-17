package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	v1 "github.com/alynlin/example/tcp/v1/client"
	"github.com/alynlin/example/tcp/v1/sequence"
	"github.com/alynlin/example/tcp/v1/server"
)

func main() {

	handler := func(method string, params []byte) ([]byte, error) {
		fmt.Println("received request:", method, string(params))

		switch method {
		case "add":
			var nums []int
			if err := json.Unmarshal(params, &nums); err != nil {
				return nil, err
			}
			sum := 0
			for _, num := range nums {
				sum += num
			}
			return []byte(fmt.Sprintf("%d", sum)), nil
		default:
			return nil, fmt.Errorf("unknown method: %s", method)
		}
	}

	server := server.NewRPCServer("localhost:8080", handler)

	go server.Start()

	fmt.Println(sequence.GenerateRequestID_danji())
	client := v1.NewRPCClient()

	rsp, err := client.Call("localhost:8080", "add", []int{1, 2, 3}, 5*time.Second)

	if err != nil {
		fmt.Println("call failed:", err)
	} else {
		fmt.Println("call success:", string(rsp))
	}

	/////////
	// 监听终止信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// 设置 5 秒超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 优雅关闭服务器
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	select {
	case <-ctx.Done():
		//todo
		log.Println("Server exited")
	}

}
