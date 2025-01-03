package models

import (
	"time"
)

// Event 事件信息
type Event struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	BlockNumber  uint64    `gorm:"index;not null"`
	TxHash       string    `gorm:"type:char(66);index;not null"`
	ContractAddr string    `gorm:"type:char(42);index;not null"`
	Topic        string    `gorm:"type:char(66);index;not null"`
	Data         []byte    `gorm:"type:blob;not null"`
	CreatedAt    time.Time `gorm:"not null"`
}

// TableName 指定表名
func (Event) TableName() string {
	return "events"
}
