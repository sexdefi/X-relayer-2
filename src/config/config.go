package config

import (
	"fmt"
	"log"

	"relayer2/src/utils"

	"github.com/spf13/viper"
)

// Config 配置结构体
type Config struct {
	// 基础配置
	StartBlock  uint64 `yaml:"start_block"`  // 开始扫描的区块号
	MaxRPS      int    `yaml:"max_rps"`      // 每秒最大请求数
	ReqInterval int    `yaml:"req_interval"` // 请求间隔(毫秒)
	WorkerNum   int    `yaml:"worker_num"`   // 交易处理线程数
	BatchSize   int    `yaml:"batch_size"`   // 批处理大小，默认100

	// RPC节点配置
	RPCs []string `yaml:"rpcs"` // RPC节点地址列表

	// MySQL配置
	MySQL struct {
		Host     string `yaml:"host"`     // 数据库地址
		Port     int    `yaml:"port"`     // 数据库端口
		User     string `yaml:"user"`     // 数据库用户名
		Password string `yaml:"password"` // 数据库密码
		Database string `yaml:"database"` // 数据库名
		Charset  string `yaml:"charset"`  // 字符集，默认utf8mb4
	} `yaml:"mysql"`

	// Redis配置
	Redis struct {
		Host     string `yaml:"host"`     // Redis地址
		Port     int    `yaml:"port"`     // Redis端口
		Password string `yaml:"password"` // Redis密码
		DB       int    `yaml:"db"`       // Redis数据库编号
		PoolSize int    `yaml:"poolsize"` // 连接池大小，默认100
	} `yaml:"redis"`

	// 业务配置
	Contracts []string `yaml:"contracts"` // 要监听的合约地址列表
	Topics    []string `yaml:"topics"`    // 要监听的事件topic列表
}

// 错误定义
var (
	ErrInvalidConfig = fmt.Errorf("无效的配置")
)

// Load 加载配置文件
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("..")
	viper.AddConfigPath("../..")

	if err := viper.ReadInConfig(); err != nil {
		return nil, utils.WrapError(err, "读取配置文件失败")
	}

	log.Printf("使用配置文件: %s", viper.ConfigFileUsed())
	log.Printf("读取到的配置文件内容: %+v", viper.AllSettings())

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, utils.WrapError(err, "解析配置文件失败")
	}

	log.Printf("解析后的配置: RPCNodes=%v", cfg.RPCs)

	// 打印cfg
	log.Printf("解析后的配置: %+v", cfg)

	// 设置默认值
	setDefaults(&cfg)

	// 验证配置
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// setDefaults 设置默认值
func setDefaults(cfg *Config) {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.MySQL.Charset == "" {
		cfg.MySQL.Charset = "utf8mb4"
	}
	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = 100
	}
}

// validate 验证配置
func (c *Config) validate() error {
	if len(c.RPCs) == 0 {
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

	// 验证MySQL配置
	if c.MySQL.Host == "" || c.MySQL.Port == 0 || c.MySQL.User == "" || c.MySQL.Database == "" {
		return utils.WrapError(ErrInvalidConfig, "MySQL配置不完整")
	}

	// 验证Redis配置
	if c.Redis.Host == "" || c.Redis.Port == 0 {
		return utils.WrapError(ErrInvalidConfig, "Redis配置不完整")
	}

	return nil
}

// GetMySQLDSN 获取MySQL连接字符串
func (c *Config) GetMySQLDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=Local",
		c.MySQL.User,
		c.MySQL.Password,
		c.MySQL.Host,
		c.MySQL.Port,
		c.MySQL.Database,
		c.MySQL.Charset,
	)
}

// GetRedisAddr 获取Redis连接地址
func (c *Config) GetRedisAddr() string {
	return fmt.Sprintf("%s:%d", c.Redis.Host, c.Redis.Port)
}
