package scanner

import (
	"context"
	"log"
	"math/big"
	"strings"
	"time"

	"relayer2/src/config"
	"relayer2/src/models"
	"relayer2/src/rpc"
	"relayer2/src/storage"
	"relayer2/src/utils"
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
	go func() {
		// 延迟3分钟启动补偿器
		startDelay := time.Minute * 3
		log.Printf("补偿器将在 %v 后启动", startDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(startDelay):
			s.comp.Start(ctx)
		}
	}()

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
	var stats struct {
		totalTx        int
		nativeTx       int
		erc20Tx        int
		unknownTx      int
		filteredTx     int
		totalEvents    int
		filteredEvents int
	}

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
		stats.totalTx++

		// 构造基础交易数据
		transaction := &models.Transaction{
			BlockNumber: block.Number,
			TxHash:      tx.Hash,
			FromAddr:    tx.From,
			ToAddr:      tx.To,
			Value:       tx.Value.String(),
			Status:      tx.Status,
			Input:       tx.Input,
			CreatedAt:   time.Now(),
		}

		// 判断交易类型并解析
		isRelevant := s.parseTransaction(tx, transaction)
		if !isRelevant {
			stats.filteredTx++
			continue
		}

		// 如果是ERC20转账，保存到ERC20转账记录表
		if transaction.TxType == models.TxTypeERC20 {
			transfer := &models.ERC20Transfer{
				BlockNumber: transaction.BlockNumber,
				TxHash:      transaction.TxHash,
				TokenAddr:   transaction.TokenAddr,
				FromAddr:    transaction.FromAddr,
				ToAddr:      transaction.ToAddr,
				Value:       transaction.Value,
				MethodID:    tx.Input[:8],
				CreatedAt:   time.Now(),
			}
			if err := s.mysql.SaveERC20Transfer(transfer); err != nil {
				log.Printf("保存ERC20转账记录失败: %v", err)
			}
		}

		// 统计交易类型
		switch transaction.TxType {
		case models.TxTypeNative:
			stats.nativeTx++
		case models.TxTypeERC20:
			stats.erc20Tx++
		default:
			stats.unknownTx++
		}

		// 发送到交易处理通道
		s.txChan <- transaction

		// 处理事件日志
		for _, log := range tx.Logs {
			stats.totalEvents++

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
					stats.filteredEvents++
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

	// 输出区块处理统计信息
	log.Printf("区块 %d 统计:\n"+
		"交易统计: 总数=%d, 主币=%d, ERC20=%d, 未知=%d, 已过滤=%d\n"+
		"事件统计: 总数=%d, 已过滤=%d",
		block.Number, stats.totalTx, stats.nativeTx, stats.erc20Tx,
		stats.unknownTx, stats.filteredTx,
		stats.totalEvents, stats.filteredEvents)

	return nil
}

// parseTransaction 解析交易并判断是否需要处理
func (s *Scanner) parseTransaction(tx *rpc.Transaction, transaction *models.Transaction) bool {
	// 判断交易类型
	// 主币转账：input为空或0x，且value > 0
	if len(tx.Input) <= 2 && tx.Value.Sign() > 0 {
		transaction.TxType = models.TxTypeNative
		// 主币转账时，From/To地址就是交易的发送和接收地址
		transaction.FromAddr = tx.From
		transaction.ToAddr = tx.To
	} else if len(tx.Input) >= 8 {
		methodID := tx.Input[:8]
		// ERC20代币转账相关方法
		switch methodID {
		case "a9059cbb": // transfer(address,uint256)
			if len(tx.Input) >= 138 {
				// 确保交易是发送到代币合约的
				if tx.To == "" {
					transaction.TxType = models.TxTypeUnknown
					break
				}
				transaction.TxType = models.TxTypeERC20
				transaction.TokenAddr = tx.To
				// 对于transfer，发送者是交易的发送者
				transaction.FromAddr = tx.From
				// 解析接收地址，需要补充0x前缀
				toAddr := "0x" + tx.Input[32:72]
				if !utils.IsValidAddress(toAddr) {
					log.Printf("无效的ERC20接收地址: txHash=%s, to=%s", tx.Hash, toAddr)
					transaction.TxType = models.TxTypeUnknown
					break
				}
				transaction.ToAddr = toAddr

				// 解析转账金额
				if value, ok := new(big.Int).SetString(tx.Input[72:], 16); ok {
					transaction.Value = value.String()
				} else {
					log.Printf("解析ERC20 transfer金额失败: txHash=%s, input=%s",
						tx.Hash, tx.Input)
					transaction.TxType = models.TxTypeUnknown
				}
			}
		case "23b872dd": // transferFrom(address,address,uint256)
			if len(tx.Input) >= 202 {
				// 确保交易是发送到代币合约的
				if tx.To == "" {
					transaction.TxType = models.TxTypeUnknown
					break
				}
				transaction.TxType = models.TxTypeERC20
				transaction.TokenAddr = tx.To
				// 解析转出地址
				fromAddr := "0x" + tx.Input[32:72]
				if !utils.IsValidAddress(fromAddr) {
					log.Printf("无效的ERC20转出地址: txHash=%s, from=%s", tx.Hash, fromAddr)
					transaction.TxType = models.TxTypeUnknown
					break
				}
				// 解析接收地址
				toAddr := "0x" + tx.Input[96:136]
				if !utils.IsValidAddress(toAddr) {
					log.Printf("无效的ERC20接收地址: txHash=%s, to=%s", tx.Hash, toAddr)
					transaction.TxType = models.TxTypeUnknown
					break
				}
				transaction.FromAddr = fromAddr
				transaction.ToAddr = toAddr

				// 解析转账金额
				if value, ok := new(big.Int).SetString(tx.Input[136:], 16); ok {
					transaction.Value = value.String()
				} else {
					log.Printf("解析ERC20 transferFrom金额失败: txHash=%s, input=%s",
						tx.Hash, tx.Input)
					transaction.TxType = models.TxTypeUnknown
				}
			}
		default:
			// 如果是向合约发送的交易，但不是transfer或transferFrom，标记为未知类型
			transaction.TxType = models.TxTypeUnknown
			transaction.FromAddr = tx.From
			transaction.ToAddr = tx.To
		}
	} else {
		transaction.TxType = models.TxTypeUnknown
		transaction.FromAddr = tx.From
		transaction.ToAddr = tx.To
	}

	// 检查是否需要处理该交易
	if len(s.cfg.Contracts) > 0 {
		// 检查合约地址
		isRelevant := false
		for _, contract := range s.cfg.Contracts {
			if strings.EqualFold(tx.To, contract) ||
				(transaction.TxType == models.TxTypeERC20 && strings.EqualFold(transaction.TokenAddr, contract)) {
				isRelevant = true
				break
			}
		}
		if !isRelevant {
			return false
		}
	}

	// 检查地址监控
	if len(s.cfg.Addresses) > 0 {
		fromAddr := strings.ToLower(transaction.FromAddr)
		toAddr := strings.ToLower(transaction.ToAddr)
		for _, addr := range s.cfg.Addresses {
			if strings.EqualFold(fromAddr, addr) || strings.EqualFold(toAddr, addr) {
				return true
			}
		}
		return false
	}

	return true
}
