package config

import (
	"fmt"

	"relayer2/src/utils"

	"github.com/spf13/viper"
)

type Config struct {
	// 基础配置
	StartBlock  uint64 `yaml:"start_block"`  // 开始扫描的区块
	MaxRPS      int    `yaml:"max_rps"`      // 每秒最大请求数
	ReqInterval int    `yaml:"req_interval"` // 请求间隔(毫秒)
	WorkerNum   int    `yaml:"worker_num"`   // 交易处理线程数

	// RPC节点配置
	RPCNodes []string `yaml:"rpc_nodes"`

	// 数据库配置
	MySQL struct {
		DSN string `yaml:"dsn"`
	} `yaml:"mysql"`

	Redis struct {
		Addr     string `yaml:"addr"`
		Password string `yaml:"password"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`

	// 合约配置
	Contracts []string `yaml:"contracts"` // 要监听的合约地址列表
	Topics    []string `yaml:"topics"`    // 要监听的事件topic列表
}

// 添加错误类型
var (
	ErrInvalidConfig = fmt.Errorf("无效的配置")
)

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		return nil, utils.WrapError(err, "读取配置文件失败")
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, utils.WrapError(err, "解析配置文件失败")
	}

	// 验证配置
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.RPCNodes) == 0 {
		return utils.WrapError(ErrInvalidConfig, "至少需要配置一个RPC节点")
	}
	if c.WorkerNum <= 0 {
		return utils.WrapError(ErrInvalidConfig, "worker数量必须大于0")
	}
	if c.MaxRPS <= 0 {
		return utils.WrapError(ErrInvalidConfig, "每秒最大请求数必须大于0")
	}
	if c.ReqInterval <= 0 {
		return utils.WrapError(ErrInvalidConfig, "请求间隔必须大于0")
	}
	if c.MySQL.DSN == "" {
		return utils.WrapError(ErrInvalidConfig, "必须配置MySQL连接信息")
	}
	if c.Redis.Addr == "" {
		return utils.WrapError(ErrInvalidConfig, "必须配置Redis地址")
	}
	return nil
}
