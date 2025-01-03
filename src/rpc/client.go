package rpc

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"relayer2/src/config"
	"relayer2/src/utils"

	"github.com/ethereum/go-ethereum/ethclient"
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
	clients := make([]*ethclient.Client, 0, len(cfg.RPCNodes))
	for _, node := range cfg.RPCNodes {
		client, err := ethclient.Dial(node)
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
				log.Printf("RPC节点不可用 [%s]: %v", c.cfg.RPCNodes[i], err)
				if newClient, err := ethclient.Dial(c.cfg.RPCNodes[i]); err == nil {
					c.clients[i] = newClient
					log.Printf("RPC节点重连成功 [%s]", c.cfg.RPCNodes[i])
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
	c.limiter.Wait()

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

	// 构造返回数据
	result := &Block{
		Number:     block.NumberU64(),
		Hash:       block.Hash().Hex(),
		ParentHash: block.ParentHash().Hex(),
		Time:       block.Time(),
	}

	// 获取区块内的所有交易
	txs := make([]*Transaction, 0, len(block.Transactions()))
	for _, tx := range block.Transactions() {
		// 获取交易回执
		receipt, err := client.TransactionReceipt(ctx, tx.Hash())
		if err != nil {
			log.Printf("获取交易回执失败 [%s]: %v", tx.Hash().Hex(), err)
			continue
		}

		// 获取交易发送方
		from, err := client.TransactionSender(ctx, tx, block.Hash(), receipt.TransactionIndex)
		if err != nil {
			log.Printf("获取交易发送方失败 [%s]: %v", tx.Hash().Hex(), err)
			continue
		}

		// 构造交易数据
		transaction := &Transaction{
			Hash:     tx.Hash().Hex(),
			From:     from.Hex(),
			To:       tx.To().Hex(),
			Value:    tx.Value(),
			Status:   receipt.Status,
			GasPrice: tx.GasPrice(),
			Gas:      tx.Gas(),
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

		txs = append(txs, transaction)
	}
	result.Transactions = txs

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
