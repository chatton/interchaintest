package cosmos

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/chatton/interchaintest/ibc"
)

// BankSend sends tokens from one account to another.
func (tn *ChainNode) BankSend(ctx context.Context, keyName string, amount ibc.WalletAmount) error {
	_, err := tn.ExecTx(ctx,
		keyName, "bank", "send", keyName,
		amount.Address, fmt.Sprintf("%s%s", amount.Amount.String(), amount.Denom),
	)
	return err
}

// GetBalance fetches the current balance for a specific account address and denom.
func (c *Chain) GetBalance(ctx context.Context, address string, denom string) (sdkmath.Int, error) {
	res, err := banktypes.NewQueryClient(c.GetNode().GrpcConn).Balance(ctx, &banktypes.QueryBalanceRequest{Address: address, Denom: denom})
	if err != nil {
		return sdkmath.Int{}, err
	}
	return res.Balance.Amount, err
}
