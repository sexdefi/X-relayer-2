package scanner

import (
	"context"
	"log"
	"strings"
	"time"

	"relayer2/src/config"
	"relayer2/src/models"
	"relayer2/src/rpc"
	"relayer2/src/storage"
	"relayer2/src/worker"
)

type Scanner struct {
	cfg       *config.Config
	rpcClient *rpc.Client
	mysql     *storage.MySQL
	redis     *storage.Redis
	txChan    chan *models.Transaction // 交易处理通道
	eventChan chan *models.Event       // 事件处理通道
	blockTime time.Duration
	comp      *worker.Compensator // 区块补偿器
}

func NewScanner(cfg *config.Config) (*Scanner, chan *models.Transaction, chan *models.Event) {
	txChan := make(chan *models.Transaction, 1000)
	eventChan := make(chan *models.Event, 1000)

	rpcClient := rpc.NewClient(cfg)
	mysql := storage.GetMySQL()
	redis := storage.GetRedis()

	return &Scanner{
		cfg:       cfg,
		rpcClient: rpcClient,
		mysql:     mysql,
		redis:     redis,
		txChan:    txChan,
		eventChan: eventChan,
		blockTime: time.Second * 12,
		comp:      worker.NewCompensator(cfg, rpcClient, mysql, redis),
	}, txChan, eventChan
}

func (s *Scanner) Start(ctx context.Context) {
	// 获取开始区块
	currentBlock := s.cfg.StartBlock
	latestBlock, err := s.redis.GetLatestBlock()
	if err != nil {
		log.Printf("获取Redis最新区块失败: %v", err)
	} else if latestBlock > currentBlock {
		currentBlock = latestBlock + 1
	}

	log.Printf("开始扫描区块，起始区块: %d", currentBlock)

	ticker := time.NewTicker(time.Duration(s.cfg.ReqInterval) * time.Millisecond)
	defer ticker.Stop()

	// 启动区块补偿器
	go s.comp.Start(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("扫描器收到停止信号")
			return
		case <-ticker.C:
			chainLatest, err := s.rpcClient.GetLatestBlockNumber(ctx)
			if err != nil {
				log.Printf("获取链上最新区块失败: %v", err)
				time.Sleep(time.Second)
				continue
			}

			if currentBlock > chainLatest {
				log.Printf("当前区块 %d 已到达链上最新区块 %d，等待 %v 后继续",
					currentBlock, chainLatest, s.blockTime)
				time.Sleep(s.blockTime)
				continue
			}

			// 获取区块
			block, err := s.rpcClient.GetBlockByNumber(ctx, currentBlock)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					log.Printf("区块 %d 尚未生成，等待 %v 后重试", currentBlock, s.blockTime)
					time.Sleep(s.blockTime)
					continue
				}
				log.Printf("获取区块 %d 失败: %v", currentBlock, err)
				time.Sleep(time.Second)
				continue
			}

			// 解析并保存区块
			if err := s.processBlock(block); err != nil {
				log.Printf("处理区块 %d 失败: %v", currentBlock, err)
				continue
			}

			// 更新Redis最新区块
			if err := s.redis.SetLatestBlock(currentBlock); err != nil {
				log.Printf("更新Redis最新区块失败: %v", err)
			}

			log.Printf("成功处理区块 %d，链上最新区块: %d", currentBlock, chainLatest)
			currentBlock++
		}
	}
}

func (s *Scanner) processBlock(block *rpc.Block) error {
	// 解析区块数据
	blockModel := &models.Block{
		BlockNumber: block.Number,
		BlockHash:   block.Hash,
		ParentHash:  block.ParentHash,
		BlockTime:   block.Time,
		TxCount:     uint(len(block.Transactions)),
		CreatedAt:   time.Now(),
	}

	// 保存区块
	if err := s.mysql.SaveBlock(blockModel); err != nil {
		return err
	}

	// 缓存区块
	if err := s.redis.CacheBlock(blockModel); err != nil {
		log.Printf("缓存区块 %d 失败: %v", blockModel.BlockNumber, err)
	}

	// 处理交易
	for _, tx := range block.Transactions {
		// 如果配置了合约地址，则只处理相关交易
		if len(s.cfg.Contracts) > 0 {
			isRelevant := false
			for _, contract := range s.cfg.Contracts {
				if tx.To == contract {
					isRelevant = true
					break
				}
			}
			if !isRelevant {
				continue
			}
		}

		// 发送到交易处理通道
		s.txChan <- &models.Transaction{
			BlockNumber: block.Number,
			TxHash:      tx.Hash,
			FromAddr:    tx.From,
			ToAddr:      tx.To,
			Value:       tx.Value.String(),
			Status:      tx.Status,
			CreatedAt:   time.Now(),
		}

		// 处理事件日志
		for _, log := range tx.Logs {
			// 如果配置了Topic，则只处理相关事件
			if len(s.cfg.Topics) > 0 {
				isRelevant := false
				for _, topic := range s.cfg.Topics {
					if log.Topics[0] == topic {
						isRelevant = true
						break
					}
				}
				if !isRelevant {
					continue
				}
			}

			// 发送到事件处理通道
			s.eventChan <- &models.Event{
				BlockNumber:  block.Number,
				TxHash:       tx.Hash,
				ContractAddr: log.Address,
				Topic:        log.Topics[0],
				Data:         log.Data,
				CreatedAt:    time.Now(),
			}
		}
	}

	return nil
}
