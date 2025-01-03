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
