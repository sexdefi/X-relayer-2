# Relayer2 - 区块链交易监控系统

一个高性能的以太坊区块链交易监控系统，支持实时扫描区块、解析交易和事件，并提供数据持久化存储。

## 主要特性

- 实时区块扫描和解析
- 支持 ERC20 转账交易识别和解析
- 支持原生代币转账监控
- 智能合约事件监听和解析
- 地址监控和过滤
- 批量处理和缓存机制
- 自动区块补偿机制
- 高性能的多线程处理
- 支持 MySQL 和 Redis 存储

## 系统要求

- Go 1.21 或更高版本
- MySQL 5.7 或更高版本
- Redis 6.0 或更高版本
- 以太坊 RPC 节点访问

## 快速开始

1. 克隆项目
```bash
git clone [项目地址]
cd relayer2
```

2. 安装依赖
```bash
go mod download
```

3. 配置系统
复制配置文件模板并修改：
```bash
cp src/config.yaml.example src/config.yaml
```

4. 启动服务
```bash
go run src/main.go
```

## 配置说明

配置文件 `src/config.yaml` 包含以下主要配置项：

```yaml
# 基础配置
startBlock: 3067795  # 开始扫描的区块号
maxRps: 100         # 每秒最大请求数
reqInterval: 100    # 请求间隔(毫秒)
workerNum: 4        # 交易处理线程数
batchSize: 100      # 批处理大小
sleepTime: 10      # 到达最新区块后的休眠时间(分钟)

# RPC节点配置
rpcs:
  - https://rpc.ankr.com/eth_holesky

# MySQL配置
mysql:
  host: "localhost"
  port: 30306
  user: "root"
  password: "password"
  database: "relayer"
  charset: "utf8mb4"

# Redis配置
redis:
  host: "localhost"
  port: 30379
  password: ""
  db: 0
  poolSize: 100

# 业务配置
contracts:
  # 要监控的合约地址列表
  # - 0x0000000000000000000000000000000000000000

topics:
  # 要监听的事件Topic列表
  # - 0x1234...

addresses:
  # 要监控的地址列表
  # - 0x0000000000000000000000000000000000000000
```

## 功能说明

### 交易监控
- 支持监控和解析 ERC20 代币转账交易（transfer/transferFrom）
- 支持监控原生代币转账
- 可配置指定地址的交易监控
- 支持合约交易监控

### 事件监听
- 支持配置特定 Topic 的事件监听
- 事件数据实时解析和存储
- 支持批量处理事件数据

### 数据存储
- MySQL：存储区块、交易和事件的详细数据
- Redis：提供快速的数据缓存和查询

### 性能优化
- 多线程并行处理
- 批量数据处理
- 请求频率限制
- 自动的区块补偿机制

## 数据库表结构

### blocks 表
- 存储区块基本信息
- 包含区块号、哈希、时间戳等

### transactions 表
- 存储交易详细信息
- 支持多种交易类型（原生转账、ERC20转账等）

### events 表
- 存储智能合约事件
- 包含合约地址、Topic、数据等

### erc20_transfers 表
- 专门存储 ERC20 代币转账记录
- 包含代币地址、转账金额等

## 开发计划

- [ ] 添加 API 接口
- [ ] 支持更多 Token 标准
- [ ] 添加监控告警功能
- [ ] 优化区块补偿机制
- [ ] 添加 WebSocket 支持

## 许可证

[许可证类型] 