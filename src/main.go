package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"relayer2/src/config"
	"relayer2/src/rpc"
	"relayer2/src/scanner"
	"relayer2/src/storage"
	"relayer2/src/worker"
)

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化存储层
	if err := storage.InitMySQL(cfg); err != nil {
		log.Fatalf("初始化MySQL失败: %v", err)
	}
	if err := storage.InitRedis(cfg); err != nil {
		log.Fatalf("初始化Redis失败: %v", err)
	}

	// 创建上下文和取消函数
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 处理信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 初始化RPC客户端
	rpcClient := rpc.NewClient(cfg)
	defer rpcClient.Close()

	// 检查RPC节点连接
	if _, err := rpcClient.GetLatestBlockNumber(ctx); err != nil {
		log.Fatalf("RPC节点连接失败: %v", err)
	}

	// 初始化扫描器
	scanner, txChan, eventChan := scanner.NewScanner(cfg)

	var wg sync.WaitGroup

	// 启动区块扫描线程
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner.Start(ctx)
	}()

	// 启动多个交易处理线程
	for i := 0; i < cfg.WorkerNum; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			worker := worker.NewTransactionWorker(workerID, cfg, txChan, eventChan)
			worker.Start(ctx)
		}(i)
	}

	// 启动区块补偿线程
	wg.Add(1)
	go func() {
		defer wg.Done()
		compensator := worker.NewCompensator(cfg, rpcClient, storage.GetMySQL(), storage.GetRedis())
		compensator.Start(ctx)
	}()

	// 等待退出信号
	<-sigChan
	log.Println("收到退出信号，开始清理资源...")
	cancel()

	// 等待所有goroutine完成
	wg.Wait()
	log.Println("程序退出")
}
