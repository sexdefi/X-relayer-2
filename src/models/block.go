package models

import (
	"time"
)

// Block 区块信息
type Block struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	BlockNumber uint64    `gorm:"uniqueIndex;not null"`
	BlockHash   string    `gorm:"type:char(66);uniqueIndex;not null"`
	ParentHash  string    `gorm:"type:char(66);not null"`
	BlockTime   uint64    `gorm:"not null"`
	TxCount     uint      `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
}

// TableName 指定表名
func (Block) TableName() string {
	return "blocks"
}

type Transaction struct {
	ID          uint64    `gorm:"primaryKey"`
	BlockNumber uint64    `gorm:"index;not null"`
	TxHash      string    `gorm:"type:char(66);uniqueIndex;not null"`
	FromAddr    string    `gorm:"type:char(42);index;not null"`
	ToAddr      string    `gorm:"type:char(42);index"`
	Value       string    `gorm:"type:varchar(78);not null"` // 十进制字符串
	Status      uint      `gorm:"not null"`                  // 1成功 0失败
	CreatedAt   time.Time `gorm:"not null"`
}

type Event struct {
	ID           uint64    `gorm:"primaryKey"`
	BlockNumber  uint64    `gorm:"index;not null"`
	TxHash       string    `gorm:"type:char(66);index;not null"`
	ContractAddr string    `gorm:"type:char(42);index;not null"`
	Topic        string    `gorm:"type:char(66);index;not null"`
	Data         []byte    `gorm:"type:blob"`
	CreatedAt    time.Time `gorm:"not null"`
}
