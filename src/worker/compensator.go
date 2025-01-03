package worker

import (
	"context"
	"log"
	"time"

	"relayer2/src/config"
	"relayer2/src/models"
	"relayer2/src/rpc"
	"relayer2/src/storage"
)

type BlockCompensator struct {
	cfg       *config.Config
	rpcClient *rpc.Client
	mysql     *storage.MySQL
	redis     *storage.Redis
}

func NewBlockCompensator(cfg *config.Config) *BlockCompensator {
	return &BlockCompensator{
		cfg:       cfg,
		rpcClient: rpc.NewClient(cfg),
		mysql:     storage.GetMySQL(),
		redis:     storage.GetRedis(),
	}
}

func (c *BlockCompensator) Start(ctx context.Context) {
	log.Println("区块补偿器启动")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("区块补偿器收到退出信号")
			return
		case <-ticker.C:
			if err := c.compensate(ctx); err != nil {
				log.Printf("区块补偿失败: %v", err)
			}
		}
	}
}

func (c *BlockCompensator) compensate(ctx context.Context) error {
	// 获取链上最新区块
	latestBlock, err := c.rpcClient.GetLatestBlockNumber(ctx)
	if err != nil {
		return err
	}

	// 获取数据库中最新区块
	dbBlock, err := c.mysql.GetLatestBlock()
	if err != nil {
		return err
	}

	// 如果数据库为空，从配置的起始区块开始
	if dbBlock == nil {
		dbBlock = &models.Block{
			BlockNumber: c.cfg.StartBlock - 1,
		}
	}

	// 检查区块连续性
	missing, err := c.mysql.GetMissingBlocks(dbBlock.BlockNumber, latestBlock)
	if err != nil {
		return err
	}

	// 处理缺失的区块
	for _, blockNum := range missing {
		block, err := c.rpcClient.GetBlockByNumber(ctx, blockNum)
		if err != nil {
			log.Printf("获取缺失区块 %d 失败: %v", blockNum, err)
			continue
		}

		// 检查区块链接是否正确
		if blockNum > c.cfg.StartBlock {
			prevBlock, err := c.mysql.GetBlockByNumber(blockNum - 1)
			if err != nil {
				log.Printf("获取前置区块 %d 失败: %v", blockNum-1, err)
				continue
			}

			// 如果前置区块的hash不匹配，说明发生了重组
			if prevBlock.BlockHash != block.ParentHash {
				if err := c.handleReorg(ctx, blockNum-1); err != nil {
					log.Printf("处理区块重组失败: %v", err)
					continue
				}
			}
		}

		// 保存区块数据
		if err := c.saveBlock(block); err != nil {
			log.Printf("保存补偿区块 %d 失败: %v", blockNum, err)
			continue
		}
	}

	return nil
}

func (c *BlockCompensator) handleReorg(ctx context.Context, fromBlock uint64) error {
	log.Printf("检测到区块重组，从区块 %d 开始处理", fromBlock)

	// 获取正确的区块数据
	block, err := c.rpcClient.GetBlockByNumber(ctx, fromBlock)
	if err != nil {
		return err
	}

	// 删除原有数据
	if err := c.mysql.DeleteBlocksFrom(fromBlock); err != nil {
		return err
	}

	// 保存新的区块数据
	return c.saveBlock(block)
}

func (c *BlockCompensator) saveBlock(block *rpc.Block) error {
	// 保存区块
	blockModel := &models.Block{
		BlockNumber: block.Number,
		BlockHash:   block.Hash,
		ParentHash:  block.ParentHash,
		BlockTime:   block.Time,
		TxCount:     uint(len(block.Transactions)),
		CreatedAt:   time.Now(),
	}

	if err := c.mysql.SaveBlock(blockModel); err != nil {
		return err
	}

	// 缓存区块
	if err := c.redis.CacheBlock(blockModel); err != nil {
		log.Printf("缓存补偿区块 %d 失败: %v", block.Number, err)
	}

	// 处理交易
	for _, tx := range block.Transactions {
		// 如果配置了合约地址，则只处理相关交易
		if len(c.cfg.Contracts) > 0 {
			isRelevant := false
			for _, contract := range c.cfg.Contracts {
				if tx.To == contract {
					isRelevant = true
					break
				}
			}
			if !isRelevant {
				continue
			}
		}

		// 保存交易
		txModel := &models.Transaction{
			BlockNumber: block.Number,
			TxHash:      tx.Hash,
			FromAddr:    tx.From,
			ToAddr:      tx.To,
			Value:       tx.Value.String(),
			Status:      tx.Status,
			CreatedAt:   time.Now(),
		}

		if err := c.mysql.SaveTransactions([]*models.Transaction{txModel}); err != nil {
			log.Printf("保存补偿交易失败 [%s]: %v", tx.Hash, err)
			continue
		}

		// 处理事件日志
		for _, eventLog := range tx.Logs {
			// 如果配置了Topic，则只处理相关事件
			if len(c.cfg.Topics) > 0 {
				isRelevant := false
				for _, topic := range c.cfg.Topics {
					if eventLog.Topics[0] == topic {
						isRelevant = true
						break
					}
				}
				if !isRelevant {
					continue
				}
			}

			// 保存事件
			event := &models.Event{
				BlockNumber:  block.Number,
				TxHash:       tx.Hash,
				ContractAddr: eventLog.Address,
				Topic:        eventLog.Topics[0],
				Data:         eventLog.Data,
				CreatedAt:    time.Now(),
			}

			if err := c.mysql.SaveEvents([]*models.Event{event}); err != nil {
				log.Printf("保存补偿事件失败 [%s]: %v", tx.Hash, err)
			}
		}
	}

	return nil
}
