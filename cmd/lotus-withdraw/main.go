package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

type WithdrawConfig struct {
	MinerAddress    string
	MultisigAddress string
	Amount          string
	FromOwner       bool
	KeystorePath    string
	SenderAddress   string
	NetworkVersion  int
}

type MessageOutput struct {
	Message    *types.Message       `json:"message"`
	SignedMsg  *types.SignedMessage `json:"signed_message,omitempty"`
	MessageCID string               `json:"message_cid"`
	TxID       uint64               `json:"tx_id"`
}

func main() {
	var cfg WithdrawConfig
	var listWallets bool

	flag.StringVar(&cfg.MinerAddress, "miner", "", "Miner address")
	flag.StringVar(&cfg.MultisigAddress, "multisig", "", "Multisig wallet address")
	flag.StringVar(&cfg.Amount, "amount", "0", "Amount to withdraw (0 for full balance)")
	flag.BoolVar(&cfg.FromOwner, "from-owner", true, "Withdraw from owner (true) or beneficiary (false)")
	flag.StringVar(&cfg.KeystorePath, "keystore", "~/.lotus/keystore", "Path to keystore")
	flag.StringVar(&cfg.SenderAddress, "sender", "", "Sender address (must be in keystore)")
	flag.IntVar(&cfg.NetworkVersion, "network-version", 18, "Network version (default: 18 for mainnet)")
	flag.BoolVar(&listWallets, "list", false, "List all wallets in keystore")
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

	if cfg.MinerAddress == "" {
		fmt.Println("Error: miner address is required")
		flag.Usage()
		os.Exit(1)
	}

	if cfg.MultisigAddress == "" {
		fmt.Println("Error: multisig address is required")
		flag.Usage()
		os.Exit(1)
	}

	if cfg.SenderAddress == "" {
		fmt.Println("Error: sender address is required")
		flag.Usage()
		os.Exit(1)
	}

	// 解析地址
	minerAddr, err := address.NewFromString(cfg.MinerAddress)
	if err != nil {
		fmt.Printf("Error parsing miner address: %v\n", err)
		os.Exit(1)
	}

	multisigAddr, err := address.NewFromString(cfg.MultisigAddress)
	if err != nil {
		fmt.Printf("Error parsing multisig address: %v\n", err)
		os.Exit(1)
	}

	senderAddr, err := address.NewFromString(cfg.SenderAddress)
	if err != nil {
		fmt.Printf("Error parsing sender address: %v\n", err)
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
	output, err := createWithdrawProposal(cfg, minerAddr, multisigAddr, senderAddr, amount)
	if err != nil {
		fmt.Printf("Error creating withdraw proposal: %v\n", err)
		os.Exit(1)
	}

	// 输出JSON格式的消息
	jsonOutput, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(jsonOutput))
}

func createWithdrawProposal(cfg WithdrawConfig, minerAddr, multisigAddr, senderAddr address.Address, amount abi.TokenAmount) (*MessageOutput, error) {
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

	fmt.Printf("创建了多签提案消息:\n")
	fmt.Printf("  发送者: %s\n", proposal.From)
	fmt.Printf("  接收者: %s\n", proposal.To)
	fmt.Printf("  方法: %d\n", proposal.Method)
	fmt.Printf("  金额: %s\n", types.FIL(proposal.Value))
	fmt.Printf("  Nonce: %d\n", proposal.Nonce)

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
