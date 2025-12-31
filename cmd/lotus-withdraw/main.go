package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	actorstypes "github.com/filecoin-project/go-state-types/actors"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/builtin"
	"github.com/filecoin-project/go-state-types/network"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors"
	lminer "github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/chain/actors/builtin/multisig"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/wallet"
)

type Config struct {
	// 通用参数
	KeystorePath   string
	SenderAddress  string
	NetworkVersion int
	Nonce          uint64
	GasLimit       int64
	GasFeeCap      string
	GasPremium     string

	// 操作类型
	Operation string // "withdraw" 或 "transfer"

	// Withdraw参数
	MinerAddress    string
	MultisigAddress string
	Amount          string
	FromOwner       bool

	// Transfer参数
	ToAddress      string
	TransferAmount string

	// 执行选项
	Execute     bool   // 是否直接执行（提交到API）
	APIURL      string // API URL
	UseMultisig bool   // 是否使用多签（false时直接转账）
}

type MessageOutput struct {
	Message    *types.Message       `json:"message"`
	SignedMsg  *types.SignedMessage `json:"signed_message,omitempty"`
	MessageCID string               `json:"message_cid"`
	TxID       uint64               `json:"tx_id"`
}

func main() {
	var cfg Config
	var listWallets bool

	// 操作类型
	flag.StringVar(&cfg.Operation, "operation", "withdraw", "Operation type: withdraw or transfer")

	// 通用参数
	flag.StringVar(&cfg.KeystorePath, "keystore", "~/.lotus/keystore", "Path to keystore")
	flag.StringVar(&cfg.SenderAddress, "sender", "", "Sender address (must be in keystore)")
	flag.IntVar(&cfg.NetworkVersion, "network-version", 18, "Network version (default: 18 for mainnet)")
	flag.Uint64Var(&cfg.Nonce, "nonce", 0, "Message nonce")
	flag.Int64Var(&cfg.GasLimit, "gas-limit", 10000000, "Gas limit")
	flag.StringVar(&cfg.GasFeeCap, "gas-feecap", "100000000", "Gas fee cap (in attoFIL)")
	flag.StringVar(&cfg.GasPremium, "gas-premium", "100000000", "Gas premium (in attoFIL)")
	flag.BoolVar(&listWallets, "list", false, "List all wallets in keystore")

	// Withdraw参数
	flag.StringVar(&cfg.MinerAddress, "miner", "", "Miner address (for withdraw)")
	flag.StringVar(&cfg.MultisigAddress, "multisig", "", "Multisig wallet address")
	flag.StringVar(&cfg.Amount, "amount", "0", "Amount to withdraw (0 for full balance)")
	flag.BoolVar(&cfg.FromOwner, "from-owner", true, "Withdraw from owner (true) or beneficiary (false)")

	// Transfer参数
	flag.StringVar(&cfg.ToAddress, "to", "", "Recipient address (for transfer)")
	flag.StringVar(&cfg.TransferAmount, "transfer-amount", "", "Amount to transfer")

	// 执行选项
	flag.BoolVar(&cfg.Execute, "execute", false, "Execute the transaction directly (submit to API)")
	flag.StringVar(&cfg.APIURL, "api-url", "https://api.node.glif.io/rpc/v1", "API URL for execution")
	flag.BoolVar(&cfg.UseMultisig, "use-multisig", true, "Use multisig wallet (false for direct transfer)")
	flag.Parse()

	// 展开keystore路径
	if cfg.KeystorePath == "~/.lotus/keystore" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		cfg.KeystorePath = filepath.Join(home, ".lotus", "keystore")
	}

	// 如果只是列出钱包
	if listWallets {
		if err := listKeystoreWallets(cfg.KeystorePath); err != nil {
			fmt.Printf("Error listing wallets: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if cfg.SenderAddress == "" {
		fmt.Println("Error: sender address is required")
		flag.Usage()
		os.Exit(1)
	}

	// 如果使用多签，需要multisig地址
	if cfg.UseMultisig && cfg.MultisigAddress == "" {
		fmt.Println("Error: multisig address is required when using multisig")
		flag.Usage()
		os.Exit(1)
	}

	// 解析发送者地址
	senderAddr, err := address.NewFromString(cfg.SenderAddress)
	if err != nil {
		fmt.Printf("Error parsing sender address: %v\n", err)
		os.Exit(1)
	}

	// 解析多签地址（如果使用多签）
	var multisigAddr address.Address
	if cfg.UseMultisig {
		multisigAddr, err = address.NewFromString(cfg.MultisigAddress)
		if err != nil {
			fmt.Printf("Error parsing multisig address: %v\n", err)
			os.Exit(1)
		}
	}

	var output *MessageOutput

	// 根据操作类型执行不同的功能
	switch cfg.Operation {
	case "withdraw":
		if cfg.MinerAddress == "" {
			fmt.Println("Error: miner address is required for withdraw operation")
			flag.Usage()
			os.Exit(1)
		}

		// 解析矿工地址
		minerAddr, err := address.NewFromString(cfg.MinerAddress)
		if err != nil {
			fmt.Printf("Error parsing miner address: %v\n", err)
			os.Exit(1)
		}

		// 解析金额
		var amount abi.TokenAmount
		if cfg.Amount == "0" {
			amount = big.Zero()
		} else {
			filAmount, err := types.ParseFIL(cfg.Amount)
			if err != nil {
				fmt.Printf("Error parsing amount: %v\n", err)
				os.Exit(1)
			}
			amount = abi.TokenAmount(filAmount)
		}

		// 创建withdraw提案
		output, err = createWithdrawProposal(cfg, minerAddr, multisigAddr, senderAddr, amount)
		if err != nil {
			fmt.Printf("Error creating withdraw proposal: %v\n", err)
			os.Exit(1)
		}

	case "transfer":
		if cfg.ToAddress == "" {
			fmt.Println("Error: recipient address (-to) is required for transfer operation")
			flag.Usage()
			os.Exit(1)
		}

		if cfg.TransferAmount == "" {
			fmt.Println("Error: transfer amount (-transfer-amount) is required for transfer operation")
			flag.Usage()
			os.Exit(1)
		}

		// 解析接收者地址
		toAddr, err := address.NewFromString(cfg.ToAddress)
		if err != nil {
			fmt.Printf("Error parsing recipient address: %v\n", err)
			os.Exit(1)
		}

		// 解析转账金额
		transferAmount, err := types.ParseFIL(cfg.TransferAmount)
		if err != nil {
			fmt.Printf("Error parsing transfer amount: %v\n", err)
			os.Exit(1)
		}

		// 根据是否使用多签选择不同的处理方式
		if cfg.UseMultisig {
			// 创建多签转账提案
			output, err = createTransferProposal(cfg, multisigAddr, toAddr, senderAddr, abi.TokenAmount(transferAmount))
			if err != nil {
				fmt.Printf("Error creating transfer proposal: %v\n", err)
				os.Exit(1)
			}
		} else {
			// 直接转账（不使用多签）
			output, err = createDirectTransfer(cfg, toAddr, senderAddr, abi.TokenAmount(transferAmount))
			if err != nil {
				fmt.Printf("Error creating direct transfer: %v\n", err)
				os.Exit(1)
			}
		}

	default:
		fmt.Printf("Error: unknown operation type: %s\n", cfg.Operation)
		fmt.Println("Supported operations: withdraw, transfer")
		flag.Usage()
		os.Exit(1)
	}

	// 输出可直接执行的curl命令
	fmt.Println("=== 可直接执行的curl命令 ===")

	// 创建JSON-RPC格式的请求
	rpcRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "Filecoin.MpoolPush",
		"params":  []interface{}{output.SignedMsg},
		"id":      1,
	}

	rpcJson, err := json.Marshal(rpcRequest)
	if err != nil {
		fmt.Printf("Error creating RPC request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("curl -X POST https://api.node.glif.io/rpc/v1 \\\n")
	fmt.Printf("  -H \"Content-Type: application/json\" \\\n")
	fmt.Printf("  -d '%s'\n", string(rpcJson))
	fmt.Println()

	// 如果设置了执行选项，直接提交到API
	if cfg.Execute {
		fmt.Println("=== 正在执行交易 ===")
		result, err := executeTransaction(cfg.APIURL, output.SignedMsg)
		if err != nil {
			fmt.Printf("执行交易失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("交易已成功提交!\n")
		fmt.Printf("消息CID: %s\n", result)
	}
}

func createWithdrawProposal(cfg Config, minerAddr, multisigAddr, senderAddr address.Address, amount abi.TokenAmount) (*MessageOutput, error) {
	fmt.Printf("开始创建withdraw多签提案...\n")
	fmt.Printf("矿工地址: %s\n", minerAddr)
	fmt.Printf("多签地址: %s\n", multisigAddr)
	fmt.Printf("发送者地址: %s\n", senderAddr)
	fmt.Printf("提取金额: %s\n", types.FIL(amount))
	fmt.Printf("从所有者提取: %v\n", cfg.FromOwner)

	// 加载keystore
	keystore, err := loadKeystore(cfg.KeystorePath)
	if err != nil {
		return nil, xerrors.Errorf("加载keystore失败: %w", err)
	}

	// 检查发送者地址是否在keystore中
	hasKey, err := keystore.WalletHas(context.Background(), senderAddr)
	if err != nil {
		return nil, xerrors.Errorf("检查keystore中的发送者地址失败: %w", err)
	}

	if !hasKey {
		return nil, xerrors.Errorf("keystore中不包含发送者地址: %s", senderAddr)
	}

	// 准备withdraw参数
	params, err := actors.SerializeParams(&lminer.WithdrawBalanceParams{
		AmountRequested: amount,
	})
	if err != nil {
		return nil, xerrors.Errorf("序列化withdraw参数失败: %w", err)
	}

	// 获取actor版本
	av, err := actorstypes.VersionForNetwork(network.Version(cfg.NetworkVersion))
	if err != nil {
		return nil, xerrors.Errorf("获取actor版本失败: %w", err)
	}

	// 创建多签消息构建器
	mb := multisig.Message(av, senderAddr)

	// 创建多签提案
	proposal, err := mb.Propose(multisigAddr, minerAddr, types.NewInt(0), builtin.MethodsMiner.WithdrawBalance, params)
	if err != nil {
		return nil, xerrors.Errorf("创建多签提案失败: %w", err)
	}

	// 设置手动输入的gas参数
	if cfg.Nonce > 0 {
		proposal.Nonce = cfg.Nonce
	}
	if cfg.GasLimit > 0 {
		proposal.GasLimit = cfg.GasLimit
	}
	if cfg.GasFeeCap != "0" {
		gasFeeCap, err := types.BigFromString(cfg.GasFeeCap)
		if err != nil {
			return nil, xerrors.Errorf("解析gas fee cap失败: %w", err)
		}
		proposal.GasFeeCap = gasFeeCap
	}
	if cfg.GasPremium != "0" {
		gasPremium, err := types.BigFromString(cfg.GasPremium)
		if err != nil {
			return nil, xerrors.Errorf("解析gas premium失败: %w", err)
		}
		proposal.GasPremium = gasPremium
	}

	fmt.Printf("创建了多签提案消息:\n")
	fmt.Printf("  发送者: %s\n", proposal.From)
	fmt.Printf("  接收者: %s\n", proposal.To)
	fmt.Printf("  方法: %d\n", proposal.Method)
	fmt.Printf("  金额: %s\n", types.FIL(proposal.Value))
	fmt.Printf("  Nonce: %d\n", proposal.Nonce)
	fmt.Printf("  GasLimit: %d\n", proposal.GasLimit)
	fmt.Printf("  GasFeeCap: %s\n", types.FIL(proposal.GasFeeCap))
	fmt.Printf("  GasPremium: %s\n", types.FIL(proposal.GasPremium))

	// 签名消息
	sig, err := keystore.WalletSign(context.Background(), senderAddr, proposal.Cid().Bytes(), api.MsgMeta{
		Type: api.MTChainMsg,
	})
	if err != nil {
		return nil, xerrors.Errorf("签名消息失败: %w", err)
	}

	// 创建签名消息
	smsg := &types.SignedMessage{
		Message:   *proposal,
		Signature: *sig,
	}

	output := &MessageOutput{
		Message:    proposal,
		SignedMsg:  smsg,
		MessageCID: proposal.Cid().String(),
		TxID:       proposal.Nonce,
	}

	fmt.Printf("成功创建多签withdraw提案!\n")
	fmt.Printf("消息CID: %s\n", output.MessageCID)
	fmt.Printf("交易ID: %d\n", output.TxID)

	return output, nil
}

// createTransferProposal 创建转账多签提案
func createTransferProposal(cfg Config, multisigAddr, toAddr, senderAddr address.Address, amount abi.TokenAmount) (*MessageOutput, error) {
	fmt.Printf("开始创建转账多签提案...\n")
	fmt.Printf("多签地址: %s\n", multisigAddr)
	fmt.Printf("接收者地址: %s\n", toAddr)
	fmt.Printf("发送者地址: %s\n", senderAddr)
	fmt.Printf("转账金额: %s\n", types.FIL(amount))

	// 加载keystore
	keystore, err := loadKeystore(cfg.KeystorePath)
	if err != nil {
		return nil, xerrors.Errorf("加载keystore失败: %w", err)
	}

	// 检查发送者地址是否在keystore中
	hasKey, err := keystore.WalletHas(context.Background(), senderAddr)
	if err != nil {
		return nil, xerrors.Errorf("检查keystore中的发送者地址失败: %w", err)
	}

	if !hasKey {
		return nil, xerrors.Errorf("keystore中不包含发送者地址: %s", senderAddr)
	}

	// 获取actor版本
	av, err := actorstypes.VersionForNetwork(network.Version(cfg.NetworkVersion))
	if err != nil {
		return nil, xerrors.Errorf("获取actor版本失败: %w", err)
	}

	// 创建多签消息构建器
	mb := multisig.Message(av, senderAddr)

	// 创建转账提案 - 直接转账，不需要特殊参数
	proposal, err := mb.Propose(multisigAddr, toAddr, amount, 0, nil)
	if err != nil {
		return nil, xerrors.Errorf("创建转账提案失败: %w", err)
	}

	// 设置手动输入的gas参数
	if cfg.Nonce > 0 {
		proposal.Nonce = cfg.Nonce
	}
	if cfg.GasLimit > 0 {
		proposal.GasLimit = cfg.GasLimit
	}
	if cfg.GasFeeCap != "0" {
		gasFeeCap, err := types.BigFromString(cfg.GasFeeCap)
		if err != nil {
			return nil, xerrors.Errorf("解析gas fee cap失败: %w", err)
		}
		proposal.GasFeeCap = gasFeeCap
	}
	if cfg.GasPremium != "0" {
		gasPremium, err := types.BigFromString(cfg.GasPremium)
		if err != nil {
			return nil, xerrors.Errorf("解析gas premium失败: %w", err)
		}
		proposal.GasPremium = gasPremium
	}

	fmt.Printf("创建了转账提案消息:\n")
	fmt.Printf("  发送者: %s\n", proposal.From)
	fmt.Printf("  接收者: %s\n", proposal.To)
	fmt.Printf("  方法: %d\n", proposal.Method)
	fmt.Printf("  金额: %s\n", types.FIL(proposal.Value))
	fmt.Printf("  Nonce: %d\n", proposal.Nonce)
	fmt.Printf("  GasLimit: %d\n", proposal.GasLimit)
	fmt.Printf("  GasFeeCap: %s\n", types.FIL(proposal.GasFeeCap))
	fmt.Printf("  GasPremium: %s\n", types.FIL(proposal.GasPremium))

	// 签名消息
	sig, err := keystore.WalletSign(context.Background(), senderAddr, proposal.Cid().Bytes(), api.MsgMeta{
		Type: api.MTChainMsg,
	})
	if err != nil {
		return nil, xerrors.Errorf("签名消息失败: %w", err)
	}

	// 创建签名消息
	smsg := &types.SignedMessage{
		Message:   *proposal,
		Signature: *sig,
	}

	output := &MessageOutput{
		Message:    proposal,
		SignedMsg:  smsg,
		MessageCID: proposal.Cid().String(),
		TxID:       proposal.Nonce,
	}

	fmt.Printf("成功创建转账多签提案!\n")
	fmt.Printf("消息CID: %s\n", output.MessageCID)
	fmt.Printf("交易ID: %d\n", output.TxID)

	return output, nil
}

// createDirectTransfer 创建直接转账（不使用多签）
func createDirectTransfer(cfg Config, toAddr, senderAddr address.Address, amount abi.TokenAmount) (*MessageOutput, error) {
	fmt.Printf("开始创建直接转账...\n")
	fmt.Printf("发送者地址: %s\n", senderAddr)
	fmt.Printf("接收者地址: %s\n", toAddr)
	fmt.Printf("转账金额: %s\n", types.FIL(amount))

	// 加载keystore
	keystore, err := loadKeystore(cfg.KeystorePath)
	if err != nil {
		return nil, xerrors.Errorf("加载keystore失败: %w", err)
	}

	// 检查发送者地址是否在keystore中
	hasKey, err := keystore.WalletHas(context.Background(), senderAddr)
	if err != nil {
		return nil, xerrors.Errorf("检查keystore中的发送者地址失败: %w", err)
	}

	if !hasKey {
		return nil, xerrors.Errorf("keystore中不包含发送者地址: %s", senderAddr)
	}

	// 创建直接转账消息
	proposal := &types.Message{
		To:    toAddr,
		From:  senderAddr,
		Value: amount,
		// Method 0 表示普通转账
		Method: 0,
		Params: nil,
	}

	// 设置手动输入的gas参数
	if cfg.Nonce > 0 {
		proposal.Nonce = cfg.Nonce
	}
	if cfg.GasLimit > 0 {
		proposal.GasLimit = cfg.GasLimit
	}
	if cfg.GasFeeCap != "0" {
		gasFeeCap, err := types.BigFromString(cfg.GasFeeCap)
		if err != nil {
			return nil, xerrors.Errorf("解析gas fee cap失败: %w", err)
		}
		proposal.GasFeeCap = gasFeeCap
	}
	if cfg.GasPremium != "0" {
		gasPremium, err := types.BigFromString(cfg.GasPremium)
		if err != nil {
			return nil, xerrors.Errorf("解析gas premium失败: %w", err)
		}
		proposal.GasPremium = gasPremium
	}

	fmt.Printf("创建了转账消息:\n")
	fmt.Printf("  发送者: %s\n", proposal.From)
	fmt.Printf("  接收者: %s\n", proposal.To)
	fmt.Printf("  方法: %d\n", proposal.Method)
	fmt.Printf("  金额: %s\n", types.FIL(proposal.Value))
	fmt.Printf("  Nonce: %d\n", proposal.Nonce)
	fmt.Printf("  GasLimit: %d\n", proposal.GasLimit)
	fmt.Printf("  GasFeeCap: %s\n", types.FIL(proposal.GasFeeCap))
	fmt.Printf("  GasPremium: %s\n", types.FIL(proposal.GasPremium))

	// 签名消息
	sig, err := keystore.WalletSign(context.Background(), senderAddr, proposal.Cid().Bytes(), api.MsgMeta{
		Type: api.MTChainMsg,
	})
	if err != nil {
		return nil, xerrors.Errorf("签名消息失败: %w", err)
	}

	// 创建签名消息
	smsg := &types.SignedMessage{
		Message:   *proposal,
		Signature: *sig,
	}

	output := &MessageOutput{
		Message:    proposal,
		SignedMsg:  smsg,
		MessageCID: proposal.Cid().String(),
		TxID:       proposal.Nonce,
	}

	fmt.Printf("成功创建直接转账消息!\n")
	fmt.Printf("消息CID: %s\n", output.MessageCID)
	fmt.Printf("交易ID: %d\n", output.TxID)

	return output, nil
}

func loadKeystore(keystorePath string) (api.Wallet, error) {
	// 检查keystore目录是否存在
	if _, err := os.Stat(keystorePath); os.IsNotExist(err) {
		return nil, xerrors.Errorf("keystore目录不存在: %s", keystorePath)
	}

	// 创建keystore钱包
	keystore := wallet.NewMemKeyStore()

	// 创建钱包
	w, err := wallet.NewWallet(keystore)
	if err != nil {
		return nil, xerrors.Errorf("创建钱包失败: %w", err)
	}

	// 从文件加载密钥
	err = loadKeysFromFile(w, keystorePath)
	if err != nil {
		return nil, xerrors.Errorf("从文件加载keystore失败: %w", err)
	}

	return w, nil
}

func loadKeysFromFile(w api.Wallet, keystorePath string) error {
	// 读取keystore目录中的所有文件
	files, err := os.ReadDir(keystorePath)
	if err != nil {
		return xerrors.Errorf("读取keystore目录失败: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// 读取密钥文件
		keyPath := filepath.Join(keystorePath, file.Name())
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			continue // 跳过无法读取的文件
		}

		// 解析密钥信息
		var keyInfo types.KeyInfo
		if err := json.Unmarshal(keyData, &keyInfo); err != nil {
			continue // 跳过无法解析的文件
		}

		// 导入密钥到钱包
		_, err = w.WalletImport(context.Background(), &keyInfo)
		if err != nil {
			continue // 跳过无法导入的密钥
		}
	}

	return nil
}

// listKeystoreWallets 列出keystore中的所有钱包
func listKeystoreWallets(keystorePath string) error {
	fmt.Printf("正在列出keystore中的钱包: %s\n", keystorePath)

	// 加载keystore
	keystore, err := loadKeystore(keystorePath)
	if err != nil {
		return xerrors.Errorf("加载keystore失败: %w", err)
	}

	// 获取所有钱包地址
	addresses, err := keystore.WalletList(context.Background())
	if err != nil {
		return xerrors.Errorf("获取钱包列表失败: %w", err)
	}

	if len(addresses) == 0 {
		fmt.Println("keystore中没有找到任何钱包")
		return nil
	}

	fmt.Printf("找到 %d 个钱包:\n", len(addresses))
	for i, addr := range addresses {
		fmt.Printf("  %d. %s\n", i+1, addr)
	}

	return nil
}

// executeTransaction 执行交易，提交到API
func executeTransaction(apiURL string, signedMsg *types.SignedMessage) (string, error) {
	// 创建JSON-RPC请求
	rpcRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "Filecoin.MpoolPush",
		"params":  []interface{}{signedMsg},
		"id":      1,
	}

	jsonData, err := json.Marshal(rpcRequest)
	if err != nil {
		return "", xerrors.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", xerrors.Errorf("创建HTTP请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", xerrors.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", xerrors.Errorf("读取响应失败: %w", err)
	}

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		return "", xerrors.Errorf("API返回错误状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	// 解析JSON-RPC响应
	var rpcResponse struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(body, &rpcResponse); err != nil {
		return "", xerrors.Errorf("解析响应失败: %w, 响应内容: %s", err, string(body))
	}

	// 检查是否有错误
	if rpcResponse.Error.Code != 0 {
		return "", xerrors.Errorf("API返回错误: [%d] %s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}

	if len(rpcResponse.Result) == 0 {
		return "", xerrors.Errorf("API响应中没有结果，完整响应: %s", string(body))
	}

	// 尝试解析result，可能是字符串或CID对象
	var resultStr string
	var cidObj struct {
		Slash string `json:"/"`
	}

	// 先尝试作为字符串解析
	if err := json.Unmarshal(rpcResponse.Result, &resultStr); err == nil && resultStr != "" {
		return resultStr, nil
	}

	// 如果失败，尝试作为CID对象解析（格式：{"/": "bafy..."}）
	if err := json.Unmarshal(rpcResponse.Result, &cidObj); err == nil && cidObj.Slash != "" {
		return cidObj.Slash, nil
	}

	// 如果都失败，返回原始JSON
	return string(rpcResponse.Result), nil
}
