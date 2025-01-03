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

type Compensator struct {
	cfg       *config.Config
	rpcClient *rpc.Client
	mysql     *storage.MySQL
	redis     *storage.Redis
}

func NewCompensator(cfg *config.Config, client *rpc.Client, mysql *storage.MySQL, redis *storage.Redis) *Compensator {
	return &Compensator{
		cfg:       cfg,
		rpcClient: client,
		mysql:     mysql,
		redis:     redis,
	}
}

func (c *Compensator) Start(ctx context.Context) {
	log.Println("区块补偿器启动")
	ticker := time.NewTicker(time.Minute) // 每分钟检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("补偿器收到停止信号")
			return
		case <-ticker.C:
			if err := c.compensate(ctx); err != nil {
				log.Printf("区块补偿失败: %v", err)
			}
		}
	}
}

func (c *Compensator) compensate(ctx context.Context) error {
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
	startBlock := c.cfg.StartBlock
	if dbBlock != nil {
		startBlock = dbBlock.BlockNumber + 1
	}

	// 检查缺失的区块
	missing, err := c.mysql.GetMissingBlocks(startBlock, latestBlock)
	if err != nil {
		return err
	}

	if len(missing) > 0 {
		log.Printf("发现 %d 个缺失区块，开始补偿", len(missing))
		for _, blockNum := range missing {
			if err := c.processBlock(ctx, blockNum); err != nil {
				log.Printf("补偿区块 %d 失败: %v", blockNum, err)
			}
		}
	}

	return nil
}

func (c *Compensator) processBlock(ctx context.Context, number uint64) error {
	block, err := c.rpcClient.GetBlockByNumber(ctx, number)
	if err != nil {
		return err
	}

	// 检查区块链接
	if number > c.cfg.StartBlock {
		prevBlock, err := c.mysql.GetBlockByNumber(number - 1)
		if err != nil {
			return err
		}
		if prevBlock != nil && prevBlock.BlockHash != block.ParentHash {
			log.Printf("检测到区块重组，从区块 %d 开始重新同步", number-1)
			if err := c.mysql.DeleteBlocksFrom(number - 1); err != nil {
				return err
			}
		}
	}

	// 保存区块数据
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

	log.Printf("成功补偿区块 %d", number)
	return nil
}
