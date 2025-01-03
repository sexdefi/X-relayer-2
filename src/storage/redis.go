package storage

import (
	"context"
	"fmt"
	"strconv"
	"sync"

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
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			PoolSize: 100,
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
	blockCacheKey  = "block:%d"     // block:区块号
	txCacheKey     = "tx:%s"        // tx:交易哈希
	blockTxsKey    = "block:txs:%d" // block:txs:区块号
	contractTxsKey = "contract:%s"  // contract:合约地址
	latestBlockKey = "latest_block"
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
