package storage

import (
	"fmt"
	"sync"

	"relayer2/src/config"
	"relayer2/src/models"
	"relayer2/src/utils"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type MySQL struct {
	db  *gorm.DB
	cfg *config.Config
	mu  sync.RWMutex
}

var (
	mysqlInstance *MySQL
	mysqlOnce     sync.Once
)

func GetMySQL() *MySQL {
	return mysqlInstance
}

func InitMySQL(cfg *config.Config) error {
	var err error
	mysqlOnce.Do(func() {
		db, e := gorm.Open(mysql.Open(cfg.MySQL.DSN), &gorm.Config{})
		if e != nil {
			err = fmt.Errorf("连接MySQL失败: %w", e)
			return
		}

		sqlDB, e := db.DB()
		if e != nil {
			err = fmt.Errorf("获取DB实例失败: %w", e)
			return
		}

		// 设置连接池
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(100)

		// 自动迁移
		if e := db.AutoMigrate(&models.Block{}, &models.Transaction{}, &models.Event{}); e != nil {
			err = fmt.Errorf("数据库迁移失败: %w", e)
			return
		}

		mysqlInstance = &MySQL{
			db:  db,
			cfg: cfg,
		}
	})
	return err
}

func (m *MySQL) SaveBlock(block *models.Block) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.db.Create(block).Error; err != nil {
		return utils.WrapError(err, fmt.Sprintf("保存区块 %d 失败", block.BlockNumber))
	}
	return nil
}

func (m *MySQL) SaveTransactions(txs []*models.Transaction) error {
	if len(txs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.db.Create(txs).Error; err != nil {
		return utils.WrapError(err, "批量保存交易失败")
	}
	return nil
}

func (m *MySQL) SaveEvents(events []*models.Event) error {
	if len(events) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.db.Create(events).Error; err != nil {
		return utils.WrapError(err, "批量保存事件失败")
	}
	return nil
}

func (m *MySQL) GetLatestBlock() (*models.Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var block models.Block
	err := m.db.Order("block_number DESC").First(&block).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &block, err
}

func (m *MySQL) GetMissingBlocks(start, end uint64) ([]uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var numbers []uint64
	err := m.db.Model(&models.Block{}).
		Where("block_number >= ? AND block_number <= ?", start, end).
		Pluck("block_number", &numbers).Error
	if err != nil {
		return nil, err
	}

	// 找出缺失的区块号
	missing := make([]uint64, 0)
	j := 0
	for i := start; i <= end; i++ {
		if j >= len(numbers) || numbers[j] != i {
			missing = append(missing, i)
		} else {
			j++
		}
	}

	return missing, nil
}

func (m *MySQL) GetBlockByNumber(number uint64) (*models.Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var block models.Block
	err := m.db.Where("block_number = ?", number).First(&block).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &block, err
}

func (m *MySQL) DeleteBlocksFrom(number uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 开启事务
	tx := m.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除区块
	if err := tx.Where("block_number >= ?", number).Delete(&models.Block{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 删除交易
	if err := tx.Where("block_number >= ?", number).Delete(&models.Transaction{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 删除事件
	if err := tx.Where("block_number >= ?", number).Delete(&models.Event{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}
