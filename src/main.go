package main

import (
	"context"
	"log"
	"sync"

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

	// 检查RPC节点连接
	rpcClient := rpc.NewClient(cfg)
	ctx := context.Background()
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
		scanner.Start()
	}()

	// 启动多个交易处理线程
	for i := 0; i < cfg.WorkerNum; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			worker := worker.NewTransactionWorker(workerID, cfg, txChan, eventChan)
			worker.Start()
		}(i)
	}

	// 启动区块补偿线程
	wg.Add(1)
	go func() {
		defer wg.Done()
		compensator := worker.NewBlockCompensator(cfg)
		compensator.Start()
	}()

	// 等待所有goroutine完成
	wg.Wait()
}
