package cosmos

import (
	types2 "github.com/chatton/interchaintest/framework/types"
	"github.com/cosmos/cosmos-sdk/types"
)

var (
	_ types2.Wallet = &CosmosWallet{}
	_ User          = &CosmosWallet{}
)

type CosmosWallet struct {
	mnemonic string
	address  []byte
	keyName  string
	chainCfg types2.Config
}

func NewWallet(keyname string, address []byte, mnemonic string, chainCfg types2.Config) types2.Wallet {
	return &CosmosWallet{
		mnemonic: mnemonic,
		address:  address,
		keyName:  keyname,
		chainCfg: chainCfg,
	}
}

func (w *CosmosWallet) KeyName() string {
	return w.keyName
}

// Get formatted address, passing in a prefix.
func (w *CosmosWallet) FormattedAddress() string {
	return types.MustBech32ifyAddressBytes(w.chainCfg.Bech32Prefix, w.address)
}

// Get mnemonic, only used for relayer wallets.
func (w *CosmosWallet) Mnemonic() string {
	return w.mnemonic
}

// Get Address with chain's prefix.
func (w *CosmosWallet) Address() []byte {
	return w.address
}

func (w *CosmosWallet) FormattedAddressWithPrefix(prefix string) string {
	return types.MustBech32ifyAddressBytes(prefix, w.address)
}
