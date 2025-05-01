package cosmos

import (
	"context"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// AuthQueryModuleAccount retrieves and returns the module account details by its name through gRPC query.
func (c *Chain) AuthQueryModuleAccount(ctx context.Context, moduleName string) (authtypes.ModuleAccount, error) {
	res, err := authtypes.NewQueryClient(c.GetNode().GrpcConn).ModuleAccountByName(ctx, &authtypes.QueryModuleAccountByNameRequest{
		Name: moduleName,
	})
	if err != nil {
		return authtypes.ModuleAccount{}, err
	}

	var modAcc authtypes.ModuleAccount
	err = c.GetCodec().Unmarshal(res.Account.Value, &modAcc)

	return modAcc, err
}

// AuthQueryModuleAddress queries the module account's address for the specified module name using the blockchain's Auth module.
// It returns the address as a string or an error if the query fails.
func (c *Chain) AuthQueryModuleAddress(ctx context.Context, moduleName string) (string, error) {
	queryRes, err := c.AuthQueryModuleAccount(ctx, moduleName)
	if err != nil {
		return "", err
	}
	return queryRes.BaseAccount.Address, nil
}

// Deprecated: use AuthQueryModuleAddress instead.
func (c *Chain) GetModuleAddress(ctx context.Context, moduleName string) (string, error) {
	return c.AuthQueryModuleAddress(ctx, moduleName)
}

// GetGovernanceAddress performs a query to get the address of the chain's x/gov module
// Deprecated: use AuthQueryModuleAddress(ctx, "gov") instead.
func (c *Chain) GetGovernanceAddress(ctx context.Context) (string, error) {
	return c.GetModuleAddress(ctx, "gov")
}
