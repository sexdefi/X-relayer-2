package storage

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"relayer2/src/config"
	"relayer2/src/models"
	"relayer2/src/utils"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	client *redis.Client
	cfg    *config.Config
}

var (
	redisInstance *Redis
	redisOnce     sync.Once
)

func GetRedis() *Redis {
	return redisInstance
}

func InitRedis(cfg *config.Config) error {
	var err error
	redisOnce.Do(func() {
		client := redis.NewClient(&redis.Options{
			Addr:     cfg.GetRedisAddr(),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			PoolSize: cfg.Redis.PoolSize,

			// 连接池配置
			MinIdleConns: 10, // 最小空闲连接数
			// MaxConnAge:   time.Hour * 4,    // 连接最大生命周期
			// IdleTimeout:  time.Minute * 30, // 空闲连接超时时间
			PoolTimeout:  time.Second * 30, // 连接池超时时间
			ReadTimeout:  time.Second * 2,  // 读取超时
			WriteTimeout: time.Second * 2,  // 写入超时
		})

		// 测试连接
		if e := client.Ping(context.Background()).Err(); e != nil {
			err = fmt.Errorf("连接Redis失败: %w", e)
			return
		}

		redisInstance = &Redis{
			client: client,
			cfg:    cfg,
		}
	})
	return err
}

const (
	// 区块相关
	blockCacheKey  = "block:%d"     // block:区块号 -> hash field存储区块信息
	latestBlockKey = "latest_block" // string类型存储最新区块号
	blockTxsKey    = "block:txs:%d" // block:txs:区块号 -> set类型存储区块内交易hash

	// 交易相关
	txCacheKey     = "tx:%s"       // tx:交易hash -> hash field存储交易信息
	contractTxsKey = "contract:%s" // contract:合约地址 -> zset类型存储合约相关交易，score为区块号
	addressTxsKey  = "address:%s"  // address:地址 -> zset类型存储地址相关交易，score为区块号

	// 事件相关
	eventKey = "event:%s:%s" // event:合约地址:topic -> zset类型存储事件，score为区块号

	// 缓存过期时间
	blockExpire = time.Hour * 24 // 区块缓存过期时间
	txExpire    = time.Hour * 24 // 交易缓存过期时间
	eventExpire = time.Hour * 24 // 事件缓存过期时间
)

func (r *Redis) CacheBlock(block *models.Block) error {
	ctx := context.Background()
	key := fmt.Sprintf(blockCacheKey, block.BlockNumber)

	if err := r.client.HSet(ctx, key,
		"block_hash", block.BlockHash,
		"parent_hash", block.ParentHash,
		"block_time", block.BlockTime,
		"tx_count", block.TxCount,
	).Err(); err != nil {
		return utils.WrapError(err, fmt.Sprintf("缓存区块 %d 失败", block.BlockNumber))
	}

	// 设置过期时间
	if err := r.client.Expire(ctx, key, blockExpire).Err(); err != nil {
		log.Printf("设置区块缓存过期时间失败 [%d]: %v", block.BlockNumber, err)
	}

	return nil
}

func (r *Redis) GetCachedBlock(number uint64) (*models.Block, error) {
	ctx := context.Background()
	key := fmt.Sprintf(blockCacheKey, number)

	data, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	// 转换数据
	txCount, _ := strconv.ParseUint(data["tx_count"], 10, 64)
	blockTime, _ := strconv.ParseUint(data["block_time"], 10, 64)

	return &models.Block{
		BlockNumber: number,
		BlockHash:   data["block_hash"],
		ParentHash:  data["parent_hash"],
		BlockTime:   blockTime,
		TxCount:     uint(txCount),
	}, nil
}

func (r *Redis) SetLatestBlock(number uint64) error {
	ctx := context.Background()
	return r.client.Set(ctx, latestBlockKey, number, 0).Err()
}

func (r *Redis) GetLatestBlock() (uint64, error) {
	ctx := context.Background()
	val, err := r.client.Get(ctx, latestBlockKey).Uint64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, utils.WrapError(err, "获取最新区块号失败")
	}
	return val, nil
}

func (r *Redis) HSet(ctx context.Context, key string, values ...interface{}) error {
	return r.client.HSet(ctx, key, values...).Err()
}

func (r *Redis) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return r.client.SAdd(ctx, key, members...).Err()
}

// CacheTransaction 缓存交易信息
func (r *Redis) CacheTransaction(tx *models.Transaction) error {
	ctx := context.Background()

	// 缓存交易详情
	txKey := fmt.Sprintf(txCacheKey, tx.TxHash)
	if err := r.client.HSet(ctx, txKey,
		"block_number", tx.BlockNumber,
		"from", tx.FromAddr,
		"to", tx.ToAddr,
		"value", tx.Value,
		"status", tx.Status,
	).Err(); err != nil {
		return err
	}
	r.client.Expire(ctx, txKey, txExpire)

	// 添加到区块交易集合
	blockTxsKey := fmt.Sprintf(blockTxsKey, tx.BlockNumber)
	r.client.SAdd(ctx, blockTxsKey, tx.TxHash)
	r.client.Expire(ctx, blockTxsKey, blockExpire)

	// 如果是合约交易，添加到合约交易集合
	if len(r.cfg.Contracts) > 0 {
		for _, contract := range r.cfg.Contracts {
			if tx.ToAddr == contract {
				r.client.ZAdd(ctx, fmt.Sprintf(contractTxsKey, contract),
					redis.Z{Score: float64(tx.BlockNumber), Member: tx.TxHash})
			}
		}
	}

	// 添加到地址交易集合
	r.client.ZAdd(ctx, fmt.Sprintf(addressTxsKey, tx.FromAddr),
		redis.Z{Score: float64(tx.BlockNumber), Member: tx.TxHash})
	if tx.ToAddr != "" {
		r.client.ZAdd(ctx, fmt.Sprintf(addressTxsKey, tx.ToAddr),
			redis.Z{Score: float64(tx.BlockNumber), Member: tx.TxHash})
	}

	return nil
}
