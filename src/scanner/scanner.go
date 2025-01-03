package scanner

import (
	"context"
	"log"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
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
	stopping  atomic.Bool
	// 添加批处理缓冲
	txBuffer    []*models.Transaction
	eventBuffer []*models.Event
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
	// 创建一个带取消的上下文
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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
			s.stopping.Store(true)
			// 取消所有进行中的操作
			cancel()
			s.quickDrain()
			// 立即返回，不等待其他操作
			return
		case <-ticker.C:
			chainLatest, err := s.rpcClient.GetLatestBlockNumber(scanCtx)
			if err != nil {
				// 如果是因为上下文取消，直接返回
				if scanCtx.Err() != nil {
					return
				}
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
			block, err := s.rpcClient.GetBlockByNumber(scanCtx, currentBlock)
			if err != nil {
				// 如果是因为上下文取消，直接返回
				if scanCtx.Err() != nil {
					return
				}
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
	if s.stopping.Load() {
		return nil
	}

	var stats struct {
		totalTx        int
		nativeTx       int
		erc20Tx        int
		unknownTx      int
		filteredTx     int
		totalEvents    int
		filteredEvents int
	}

	// 重置缓冲区
	s.txBuffer = s.txBuffer[:0]
	s.eventBuffer = s.eventBuffer[:0]

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
		if s.stopping.Load() {
			return nil
		}

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

		// 添加到缓冲区
		s.txBuffer = append(s.txBuffer, transaction)
		// 如果缓冲区达到批处理大小，则发送
		if len(s.txBuffer) >= s.cfg.BatchSize {
			if err := s.flushTransactions(); err != nil {
				log.Printf("批量保存交易失败: %v", err)
			}
		}

		// 处理事件日志
		for _, eventLog := range tx.Logs {
			if s.stopping.Load() {
				return nil
			}

			stats.totalEvents++

			// 如果配置了Topic，则只处理相关事件
			if len(s.cfg.Topics) > 0 {
				isRelevant := false
				for _, topic := range s.cfg.Topics {
					if len(eventLog.Topics) > 0 && eventLog.Topics[0] == topic {
						isRelevant = true
						break
					}
				}
				if !isRelevant {
					stats.filteredEvents++
					continue
				}
			}

			// 添加到缓冲区
			// 确保事件至少有一个topic
			if len(eventLog.Topics) == 0 {
				continue
			}

			s.eventBuffer = append(s.eventBuffer, &models.Event{
				BlockNumber:  block.Number,
				TxHash:       tx.Hash,
				ContractAddr: eventLog.Address,
				Topic:        eventLog.Topics[0],
				Data:         eventLog.Data,
				CreatedAt:    time.Now(),
			})
			// 如果缓冲区达到批处理大小，则发送
			if len(s.eventBuffer) >= s.cfg.BatchSize {
				if err := s.flushEvents(); err != nil {
					log.Printf("批量保存事件失败: %v", err)
				}
			}
		}
	}

	// 处理完区块后，刷新剩余的缓冲数据
	if err := s.flushBuffers(); err != nil {
		log.Printf("刷新缓冲区失败: %v", err)
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

// flushBuffers 刷新所有缓冲区
func (s *Scanner) flushBuffers() error {
	if err := s.flushTransactions(); err != nil {
		return err
	}
	return s.flushEvents()
}

// flushTransactions 批量处理交易
func (s *Scanner) flushTransactions() error {
	if s.stopping.Load() {
		return nil
	}

	if len(s.txBuffer) == 0 {
		return nil
	}

	// 批量保存到数据库
	if err := s.mysql.SaveTransactions(s.txBuffer); err != nil {
		return err
	}

	// 发送到处理通道
	for _, tx := range s.txBuffer {
		select {
		case s.txChan <- tx:
		case <-time.After(time.Second):
			if s.stopping.Load() {
				return nil
			}
			log.Printf("发送交易到处理通道超时: %s", tx.TxHash)
		}
	}

	// 清空缓冲区
	s.txBuffer = s.txBuffer[:0]
	return nil
}

// flushEvents 批量处理事件
func (s *Scanner) flushEvents() error {
	if s.stopping.Load() {
		return nil
	}

	if len(s.eventBuffer) == 0 {
		return nil
	}

	// 批量保存到数据库
	if err := s.mysql.SaveEvents(s.eventBuffer); err != nil {
		return err
	}

	// 发送到处理通道
	for _, event := range s.eventBuffer {
		select {
		case s.eventChan <- event:
		case <-time.After(time.Second):
			if s.stopping.Load() {
				return nil
			}
			log.Printf("发送事件到处理通道超时: %s", event.TxHash)
		}
	}

	// 清空缓冲区
	s.eventBuffer = s.eventBuffer[:0]
	return nil
}

// parseTransaction 解析交易并判断是否需要处理
func (s *Scanner) parseTransaction(tx *rpc.Transaction, transaction *models.Transaction) bool {
	// 先设置基础信息
	transaction.FromAddr = tx.From
	transaction.ToAddr = tx.To

	// 处理input数据，去除可能存在的0x前缀
	input := strings.TrimPrefix(tx.Input, "0x")

	// 添加输入数据长度检查的日志
	log.Printf("解析交易 [%s]: 原始input=%s, 处理后input长度=%d", tx.Hash, tx.Input, len(input))

	// 判断是否是ERC20转账
	if len(input) >= 8 {
		methodID := input[:8]
		log.Printf("交易 [%s]: 方法ID=%s", tx.Hash, methodID)

		switch methodID {
		case "a9059cbb": // transfer(address,uint256)
			log.Printf("交易 [%s]: 检测到ERC20 transfer方法", tx.Hash)
			if len(input) == 136 && tx.To != "" {
				transaction.TxType = models.TxTypeERC20
				transaction.TokenAddr = tx.To
				transaction.FromAddr = tx.From

				// 解析接收地址
				toAddr := "0x" + input[32:72]
				log.Printf("交易 [%s]: 解析ERC20接收地址=%s", tx.Hash, toAddr)
				if !utils.IsValidAddress(toAddr) {
					log.Printf("无效的ERC20接收地址: txHash=%s, to=%s", tx.Hash, toAddr)
					transaction.TxType = models.TxTypeUnknown
					return true
				}
				transaction.ToAddr = toAddr

				// 解析转账金额 (只取32字节/64个字符的金额数据)
				amountHex := input[72:136]
				log.Printf("交易 [%s]: 解析ERC20转账金额数据=%s", tx.Hash, amountHex)
				if value, ok := new(big.Int).SetString(amountHex, 16); ok {
					transaction.Value = value.String()
					log.Printf("交易 [%s]: ERC20转账金额解析结果=%s", tx.Hash, transaction.Value)
					return true // 成功解析ERC20转账
				}
				log.Printf("解析ERC20 transfer金额失败: txHash=%s, input=%s", tx.Hash, input)
				transaction.TxType = models.TxTypeUnknown
				return true
			} else {
				log.Printf("交易 [%s]: input长度不足或合约地址为空: len=%d, to=%s", tx.Hash, len(input), tx.To)
			}
		case "23b872dd": // transferFrom(address,address,uint256)
			if len(input) >= 200 && tx.To != "" { // 调整长度检查，考虑去除0x前缀
				transaction.TxType = models.TxTypeERC20
				transaction.TokenAddr = tx.To

				// 解析转出地址
				fromAddr := "0x" + input[32:72]
				if !utils.IsValidAddress(fromAddr) {
					log.Printf("无效的ERC20转出地址: txHash=%s, from=%s", tx.Hash, fromAddr)
					transaction.TxType = models.TxTypeUnknown
					return true
				}

				// 解析接收地址
				toAddr := "0x" + input[96:136]
				if !utils.IsValidAddress(toAddr) {
					log.Printf("无效的ERC20接收地址: txHash=%s, to=%s", tx.Hash, toAddr)
					transaction.TxType = models.TxTypeUnknown
					return true
				}

				transaction.FromAddr = fromAddr
				transaction.ToAddr = toAddr

				// 解析转账金额
				if value, ok := new(big.Int).SetString(input[136:200], 16); ok { // 限制金额数据长度
					transaction.Value = value.String()
					return true // 成功解析ERC20转账
				}
				log.Printf("解析ERC20 transferFrom金额失败: txHash=%s, input=%s", tx.Hash, input)
				transaction.TxType = models.TxTypeUnknown
				return true
			}
		}
	}

	// 如果不是ERC20转账，检查是否是主币转账
	if len(input) <= 2 && tx.Value.Sign() > 0 {
		transaction.TxType = models.TxTypeNative
		return true
	} else {
		transaction.TxType = models.TxTypeUnknown
	}

	// 检查是否需要处理该交易
	if len(s.cfg.Contracts) > 0 {
		for _, contract := range s.cfg.Contracts {
			if strings.EqualFold(tx.To, contract) {
				return true
			}
		}
		return false
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

// quickDrain 快速清空通道
func (s *Scanner) quickDrain() {
	// 不再等待任何操作完成
	s.stopping.Store(true)

	// 创建一个等待组来并行关闭连接
	var wg sync.WaitGroup
	wg.Add(2)

	// 并行关闭MySQL和Redis连接
	go func() {
		defer wg.Done()
		if s.mysql != nil {
			s.mysql.Close()
		}
	}()

	go func() {
		defer wg.Done()
		if s.redis != nil {
			s.redis.Close()
		}
	}()

	// 立即关闭通道
	close(s.txChan)
	close(s.eventChan)

	// 直接丢弃所有缓冲数据
	s.txBuffer = nil
	s.eventBuffer = nil

	// 等待所有连接关闭，最多等待5秒
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("所有连接已关闭")
	case <-time.After(time.Second * 5):
		log.Println("关闭连接超时")
	}

	log.Println("已关闭所有通道并清理缓冲区")
}
