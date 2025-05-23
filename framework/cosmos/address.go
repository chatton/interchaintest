package cosmos

import (
	"errors"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// AccAddressFromBech32 creates an AccAddress from a Bech32 string.
// https://github.com/cosmos/cosmos-sdk/blob/v0.50.2/types/address.go#L193-L212
func (c *Chain) AccAddressFromBech32(address string) (addr sdk.AccAddress, err error) {
	if len(strings.TrimSpace(address)) == 0 {
		return sdk.AccAddress{}, errors.New("empty address string is not allowed")
	}

	bz, err := sdk.GetFromBech32(address, c.Config().Bech32Prefix)
	if err != nil {
		return nil, err
	}

	err = sdk.VerifyAddressFormat(bz)
	if err != nil {
		return nil, err
	}

	return bz, nil
}

func (c *Chain) AccAddressToBech32(addr sdk.AccAddress) (string, error) {
	return bech32.ConvertAndEncode(c.Config().Bech32Prefix, addr)
}
