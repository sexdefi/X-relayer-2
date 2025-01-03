package worker

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"relayer2/src/config"
	"relayer2/src/models"
	"relayer2/src/storage"
)

type TransactionWorker struct {
	id        int
	cfg       *config.Config
	mysql     *storage.MySQL
	redis     *storage.Redis
	txChan    chan *models.Transaction
	eventChan chan *models.Event
	wg        sync.WaitGroup
}

func NewTransactionWorker(id int, cfg *config.Config, txChan chan *models.Transaction, eventChan chan *models.Event) *TransactionWorker {
	return &TransactionWorker{
		id:        id,
		cfg:       cfg,
		mysql:     storage.GetMySQL(),
		redis:     storage.GetRedis(),
		txChan:    txChan,
		eventChan: eventChan,
	}
}

func (w *TransactionWorker) Start(ctx context.Context) {
	log.Printf("交易处理worker[%d]启动", w.id)

	// 批量处理缓冲区
	txBatch := make([]*models.Transaction, 0, 100)
	eventBatch := make([]*models.Event, 0, 100)

	// 定时刷新ticker
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tx := <-w.txChan:
			// 添加到批处理缓冲区
			txBatch = append(txBatch, tx)

			// 如果达到批处理大小，立即处理
			if len(txBatch) >= 100 {
				w.processBatch(txBatch, eventBatch)
				txBatch = txBatch[:0]
				eventBatch = eventBatch[:0]
			}

		case event := <-w.eventChan:
			// 添加到批处理缓冲区
			eventBatch = append(eventBatch, event)

		case <-ticker.C:
			// 定时处理剩余的交易和事件
			if len(txBatch) > 0 || len(eventBatch) > 0 {
				w.processBatch(txBatch, eventBatch)
				txBatch = txBatch[:0]
				eventBatch = eventBatch[:0]
			}
		}
	}
}

func (w *TransactionWorker) processBatch(txs []*models.Transaction, events []*models.Event) {
	ctx := context.Background()

	// 保存交易
	if len(txs) > 0 {
		if err := w.mysql.SaveTransactions(txs); err != nil {
			log.Printf("Worker[%d] 保存交易失败: %v", w.id, err)
		}

		// 缓存交易
		for _, tx := range txs {
			key := fmt.Sprintf("tx:%s", tx.TxHash)
			if err := w.redis.HSet(ctx, key,
				"block_number", tx.BlockNumber,
				"from", tx.FromAddr,
				"to", tx.ToAddr,
				"value", tx.Value,
				"status", tx.Status,
			); err != nil {
				log.Printf("Worker[%d] 缓存交易失败 [%s]: %v", w.id, tx.TxHash, err)
			}
		}
	}

	// 保存事件
	if len(events) > 0 {
		if err := w.mysql.SaveEvents(events); err != nil {
			log.Printf("Worker[%d] 保存事件失败: %v", w.id, err)
		}

		// 缓存事件
		for _, event := range events {
			key := fmt.Sprintf("event:%s:%s", event.ContractAddr, event.Topic)
			if err := w.redis.SAdd(ctx, key, event.TxHash); err != nil {
				log.Printf("Worker[%d] 缓存事件失败 [%s]: %v", w.id, event.TxHash, err)
			}
		}
	}
}
