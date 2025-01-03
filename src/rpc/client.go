package rpc

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"relayer2/src/config"
	"relayer2/src/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type Client struct {
	cfg         *config.Config
	clients     []*ethclient.Client
	currentNode int
	mu          sync.Mutex
	limiter     *utils.RateLimiter
	healthCheck *time.Ticker
}

func NewClient(cfg *config.Config) *Client {
	clients := make([]*ethclient.Client, 0, len(cfg.RPCs))
	for _, node := range cfg.RPCs {
		// 创建自定义的 HTTP 客户端
		httpClient := &http.Client{
			Timeout: time.Second * 30,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxConnsPerHost:     100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  true, // 禁用自动压缩，让 RPC 客户端自己处理
			},
		}

		rpcClient, err := rpc.DialOptions(context.Background(), node,
			rpc.WithHTTPClient(httpClient),
		)
		if err != nil {
			log.Printf("RPC节点连接失败 [%s]: %v", node, err)
			continue
		}

		client := ethclient.NewClient(rpcClient)
		if err != nil {
			log.Printf("RPC节点连接失败 [%s]: %v", node, err)
			continue
		}
		clients = append(clients, client)
	}

	if len(clients) == 0 {
		log.Fatal("没有可用的RPC节点")
	}

	c := &Client{
		cfg:         cfg,
		clients:     clients,
		limiter:     utils.NewRateLimiter(cfg.MaxRPS, cfg.MaxRPS*2),
		healthCheck: time.NewTicker(time.Minute),
	}

	go c.startHealthCheck()

	return c
}

func (c *Client) startHealthCheck() {
	for range c.healthCheck.C {
		c.mu.Lock()
		for i := 0; i < len(c.clients); i++ {
			client := c.clients[i]
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := client.BlockNumber(ctx)
			cancel()

			if err != nil {
				log.Printf("RPC节点不可用 [%s]: %v", c.cfg.RPCs[i], err)
				if newClient, err := ethclient.Dial(c.cfg.RPCs[i]); err == nil {
					c.clients[i] = newClient
					log.Printf("RPC节点重连成功 [%s]", c.cfg.RPCs[i])
				}
			}
		}
		c.mu.Unlock()
	}
}

func (c *Client) Close() {
	c.healthCheck.Stop()
	for _, client := range c.clients {
		client.Close()
	}
}

func (c *Client) GetBlockByNumber(ctx context.Context, number uint64) (*Block, error) {
	log.Printf("开始处理区块 %d", number)
	c.limiter.Wait()

	// 设置最大重试次数
	maxRetries := 3
	retryInterval := time.Second * 2

	c.mu.Lock()
	client := c.clients[c.currentNode]
	c.currentNode = (c.currentNode + 1) % len(c.clients)
	c.mu.Unlock()

	// 获取区块
	block, err := client.BlockByNumber(ctx, big.NewInt(int64(number)))
	if err != nil {
		return nil, utils.WrapError(err, fmt.Sprintf("获取区块 %d 失败", number))
	}
	if block == nil {
		return nil, utils.WrapError(utils.ErrBlockNotFound, fmt.Sprintf("区块 %d 不存在", number))
	}

	log.Printf("区块 %d 包含 %d 笔交易", number, len(block.Transactions()))

	// 构造返回数据
	result := &Block{
		Number:     block.NumberU64(),
		Hash:       block.Hash().Hex(),
		ParentHash: block.ParentHash().Hex(),
		Time:       block.Time(),
	}

	// 获取区块内的所有交易
	txs := make([]*Transaction, 0, len(block.Transactions()))
	batchSize := 100
	transactions := block.Transactions()
	totalLogs := 0
	addressesMap := make(map[string]bool)
	for _, addr := range c.cfg.Addresses {
		addressesMap[strings.ToLower(addr)] = true
	}

	for i := 0; i < len(transactions); i += batchSize {
		end := i + batchSize
		if end > len(transactions) {
			end = len(transactions)
		}

		log.Printf("区块 %d: 处理第 %d-%d 笔交易", number, i+1, end)
		// 处理当前批次的交易
		for _, tx := range transactions[i:end] {
			// 添加重试逻辑
			var receipt *types.Receipt
			var from common.Address

			for retry := 0; retry < maxRetries; retry++ {
				if retry > 0 {
					log.Printf("重试获取交易回执 [%s] 第 %d 次", tx.Hash().Hex(), retry)
					time.Sleep(retryInterval)

					// 切换到下一个RPC节点
					c.mu.Lock()
					client = c.clients[c.currentNode]
					c.currentNode = (c.currentNode + 1) % len(c.clients)
					c.mu.Unlock()
				}

				var err error
				receipt, err = client.TransactionReceipt(ctx, tx.Hash())
				if err == nil {
					from, err = client.TransactionSender(ctx, tx, block.Hash(), receipt.TransactionIndex)
					if err == nil {
						break
					}
				}

				if retry == maxRetries-1 {
					log.Printf("获取交易回执失败 [%s]: %v", tx.Hash().Hex(), err)
					continue
				}
			}

			if receipt == nil {
				continue
			}

			// 构造交易数据
			transaction := &Transaction{
				Hash:     tx.Hash().Hex(),
				From:     from.Hex(),
				To:       getTxTo(tx),
				Value:    tx.Value(),
				Status:   receipt.Status,
				GasPrice: tx.GasPrice(),
				Gas:      tx.Gas(),
			}

			// 如果配置了地址监控，则只保存相关地址的交易
			if len(addressesMap) > 0 {
				fromAddr := strings.ToLower(transaction.From)
				toAddr := strings.ToLower(transaction.To)
				if !addressesMap[fromAddr] && !addressesMap[toAddr] {
					continue
				}
			}

			// 处理事件日志
			logs := make([]*Log, 0, len(receipt.Logs))
			for _, eventLog := range receipt.Logs {
				topics := make([]string, 0, len(eventLog.Topics))
				for _, topic := range eventLog.Topics {
					topics = append(topics, topic.Hex())
				}

				logs = append(logs, &Log{
					Address: eventLog.Address.Hex(),
					Topics:  topics,
					Data:    eventLog.Data,
				})
			}
			transaction.Logs = logs

			totalLogs += len(logs)
			txs = append(txs, transaction)
		}

		// 每批处理完后等待一下，避免请求过于频繁
		time.Sleep(time.Duration(c.cfg.ReqInterval) * time.Millisecond)
	}
	result.Transactions = txs

	log.Printf("区块 %d 处理完成: 共处理 %d 笔交易 (已过滤), %d 个事件",
		number, len(txs), totalLogs)

	return result, nil
}

// GetLatestBlockNumber 获取最新区块号
func (c *Client) GetLatestBlockNumber(ctx context.Context) (uint64, error) {
	c.limiter.Wait()

	c.mu.Lock()
	client := c.clients[c.currentNode]
	c.currentNode = (c.currentNode + 1) % len(c.clients)
	c.mu.Unlock()

	number, err := client.BlockNumber(ctx)
	if err != nil {
		return 0, utils.WrapError(err, "获取最新区块号失败")
	}

	return number, nil
}

// getTxTo 安全地获取交易的接收地址
func getTxTo(tx *types.Transaction) string {
	if tx.To() == nil {
		return "" // 合约创建交易
	}
	return tx.To().Hex()
}
