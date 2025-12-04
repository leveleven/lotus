# Lotus Multisig 工具

这是一个用于Filecoin多签钱包操作的工具。该工具使用本地keystore中的钱包来创建和签名多签提案消息。

## 功能特性

- 列出keystore中的所有钱包地址
- 创建miner withdraw的多签提案
- 创建转账的多签提案
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
  -operation withdraw \
  -miner f0123456 \
  -multisig f0987654 \
  -sender f0111111 \
  -amount 1000 \
  -nonce 5 \
  -gas-limit 10000000 \
  -gas-feecap 100000000 \
  -gas-premium 100000000
```

### 3. 创建转账多签提案

```bash
go run cmd/lotus-withdraw/main.go \
  -operation transfer \
  -multisig f0987654 \
  -sender f0111111 \
  -to f0222222 \
  -transfer-amount 1000 \
  -nonce 5 \
  -gas-limit 10000000 \
  -gas-feecap 100000000 \
  -gas-premium 100000000
```

#### 参数说明

**通用参数：**
- `-operation`: 操作类型，withdraw或transfer（默认：withdraw）
- `-keystore`: keystore目录路径（默认：~/.lotus/keystore）
- `-sender`: 发送者地址，必须在keystore中（必需）
- `-multisig`: 多签钱包地址（必需）
- `-network-version`: 网络版本（默认：18，主网）
- `-nonce`: 消息nonce值（默认：0，自动生成）
- `-gas-limit`: gas限制（默认：10000000）
- `-gas-feecap`: gas费用上限，以attoFIL为单位（默认：100000000）
- `-gas-premium`: gas溢价，以attoFIL为单位（默认：100000000）

**Withdraw参数：**
- `-miner`: 矿工地址（withdraw操作必需）
- `-amount`: 提取金额，使用0表示提取全部可用余额（默认：0）
- `-from-owner`: 是否从所有者账户提取，false表示从受益人账户提取（默认：true）

**Transfer参数：**
- `-to`: 接收者地址（transfer操作必需）
- `-transfer-amount`: 转账金额（transfer操作必需）

### 3. 输出格式

工具会输出JSON格式的消息，包含：

```json
{
  "message": {
    "Version": 0,
    "To": "f0123456",
    "From": "f0987654",
    "Nonce": 5,
    "Value": "0",
    "GasLimit": 10000000,
    "GasFeeCap": "100000000",
    "GasPremium": "100000000",
    "Method": 16,
    "Params": "..."
  },
  "signed_message": {
    "Message": {...},
    "Signature": {...}
  },
  "message_cid": "bafy2bzace...",
  "tx_id": 5
}
```

### 4. 提交到公共API

工具会自动生成可直接复制粘贴的curl命令，包括：

- **主网API**: https://api.node.glif.io/rpc/v1

直接复制输出的curl命令即可执行。

## 注意事项

1. 确保keystore目录中包含发送者地址的私钥
2. 发送者地址必须是多签钱包的签名者之一
3. 网络版本需要与目标网络匹配（主网：18，测试网：21）
4. 生成的提案需要其他签名者审批后才能执行
5. 建议在提交前验证消息内容的正确性

### Gas参数建议

- **Nonce**: 应该是发送者地址的下一个nonce值，可以通过API查询
- **GasLimit**: 对于多签提案，通常设置为 1000000-2000000
- **GasFeeCap**: 建议设置为当前网络的基础费用上限，如 1000000000 attoFIL (1 nanoFIL)
- **GasPremium**: 建议设置为 100000000 attoFIL (0.1 nanoFIL) 或更高以确保快速确认

如果不指定gas参数，工具会使用默认值，但建议手动设置以确保交易能够成功执行。

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
$ go run cmd/lotus-withdraw/main.go -operation withdraw -miner f0123456 -multisig f0987654 -sender f0111111 -amount 1000 -nonce 5 -gas-limit 10000000 -gas-feecap 100000000 -gas-premium 100000000
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
  Nonce: 5
  GasLimit: 10000000
  GasFeeCap: 100000000 attoFIL
  GasPremium: 100000000 attoFIL
成功创建多签withdraw提案!
消息CID: bafy2bzace...
交易ID: 5

=== JSON消息 ===
{
  "message": {...},
  "signed_message": {...},
  "message_cid": "bafy2bzace...",
  "tx_id": 5
}

=== 可直接执行的curl命令 ===
curl -X POST https://api.node.glif.io/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"Filecoin.MpoolPush","params":[{"Message":{...},"Signature":{...}}],"id":1}'
```

### 创建转账提案
```bash
$ go run cmd/lotus-withdraw/main.go -operation transfer -multisig f0987654 -sender f0111111 -to f0222222 -transfer-amount 1000 -nonce 5 -gas-limit 10000000 -gas-feecap 100000000 -gas-premium 100000000
开始创建转账多签提案...
多签地址: f0987654
接收者地址: f0222222
发送者地址: f0111111
转账金额: 1000 FIL
创建了转账提案消息:
  发送者: f0111111
  接收者: f0222222
  方法: 0
  金额: 1000 FIL
  Nonce: 5
  GasLimit: 10000000
  GasFeeCap: 100000000 attoFIL
  GasPremium: 100000000 attoFIL
成功创建转账多签提案!
消息CID: bafy2bzace...
交易ID: 5

=== JSON消息 ===
{
  "message": {...},
  "signed_message": {...},
  "message_cid": "bafy2bzace...",
  "tx_id": 5
}

=== 可直接执行的curl命令 ===
curl -X POST https://api.node.glif.io/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"Filecoin.MpoolPush","params":[{"Message":{...},"Signature":{...}}],"id":1}'
``` 