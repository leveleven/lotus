# Lotus Withdraw 工具

这是一个用于从Filecoin矿工账户提取余额的多签提案工具。该工具使用本地keystore中的钱包来创建和签名多签提案消息。

## 功能特性

- 列出keystore中的所有钱包地址
- 创建miner withdraw的多签提案
- 支持从所有者或受益人账户提取
- 生成JSON格式的消息供手动提交到公共API

## 使用方法

### 1. 列出钱包

```bash
go run cmd/lotus-withdraw/main.go -list
```

这将显示keystore目录中的所有钱包地址。

### 2. 创建withdraw多签提案

```bash
go run cmd/lotus-withdraw/main.go \
  -miner f0123456 \
  -multisig f0987654 \
  -sender f0111111 \
  -amount 1000
```

#### 参数说明

- `-miner`: 矿工地址（必需）
- `-multisig`: 多签钱包地址（必需）
- `-sender`: 发送者地址，必须在keystore中（必需）
- `-amount`: 提取金额，使用0表示提取全部可用余额（默认：0）
- `-from-owner`: 是否从所有者账户提取，false表示从受益人账户提取（默认：true）
- `-keystore`: keystore目录路径（默认：~/.lotus/keystore）
- `-network-version`: 网络版本（默认：18，主网）

### 3. 输出格式

工具会输出JSON格式的消息，包含：

```json
{
  "message": {
    "Version": 0,
    "To": "f0123456",
    "From": "f0987654",
    "Nonce": 0,
    "Value": "0",
    "GasLimit": 0,
    "GasFeeCap": "0",
    "GasPremium": "0",
    "Method": 16,
    "Params": "..."
  },
  "signed_message": {
    "Message": {...},
    "Signature": {...}
  },
  "message_cid": "bafy2bzace...",
  "tx_id": 0
}
```

### 4. 提交到公共API

将生成的JSON消息提交到Filecoin公共API：

```bash
curl -X POST https://api.node.glif.io/rpc/v0 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "Filecoin.MpoolPush",
    "params": [{"Message": {...}, "Signature": {...}}],
    "id": 1
  }'
```

## 注意事项

1. 确保keystore目录中包含发送者地址的私钥
2. 发送者地址必须是多签钱包的签名者之一
3. 网络版本需要与目标网络匹配（主网：18，测试网：21）
4. 生成的提案需要其他签名者审批后才能执行
5. 建议在提交前验证消息内容的正确性

## 错误处理

- 如果keystore目录不存在，会显示相应错误
- 如果发送者地址不在keystore中，会显示相应错误
- 如果参数格式不正确，会显示相应错误

## 示例

### 列出所有钱包
```bash
$ go run cmd/lotus-withdraw/main.go -list
正在列出keystore中的钱包: /home/user/.lotus/keystore
找到 3 个钱包:
  1. f0123456
  2. f0987654
  3. f0111111
```

### 创建withdraw提案
```bash
$ go run cmd/lotus-withdraw/main.go -miner f0123456 -multisig f0987654 -sender f0111111 -amount 1000
开始创建withdraw多签提案...
矿工地址: f0123456
多签地址: f0987654
发送者地址: f0111111
提取金额: 1000 FIL
从所有者提取: true
创建了多签提案消息:
  发送者: f0111111
  接收者: f0123456
  方法: 16
  金额: 0 FIL
  Nonce: 0
成功创建多签withdraw提案!
消息CID: bafy2bzace...
交易ID: 0
{
  "message": {...},
  "signed_message": {...},
  "message_cid": "bafy2bzace...",
  "tx_id": 0
}
``` 