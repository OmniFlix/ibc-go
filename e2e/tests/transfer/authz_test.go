package transfer

import (
	"context"
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	test "github.com/strangelove-ventures/ibctest/v6/testutil"
	"github.com/stretchr/testify/suite"

	"github.com/cosmos/ibc-go/e2e/testsuite"
	"github.com/cosmos/ibc-go/e2e/testvalues"
	transfertypes "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
)

func TestAuthzTransferTestSuite(t *testing.T) {
	suite.Run(t, new(AuthzTransferTestSuite))
}

type AuthzTransferTestSuite struct {
	testsuite.E2ETestSuite
}

func (suite *AuthzTransferTestSuite) TestAuthz_MsgTransfer_Succeeds() {
	t := suite.T()
	ctx := context.TODO()

	relayer, channelA := suite.SetupChainsRelayerAndChannel(ctx, transferChannelOptions())
	chainA, chainB := suite.GetChains()

	chainADenom := chainA.Config().Denom

	granterWallet := suite.CreateUserOnChainA(ctx, testvalues.StartingTokenAmount)
	granterAddress := granterWallet.Bech32Address(chainA.Config().Bech32Prefix)

	granteeWallet := suite.CreateUserOnChainA(ctx, testvalues.StartingTokenAmount)
	granteeAddress := granteeWallet.Bech32Address(chainA.Config().Bech32Prefix)

	receiverWallet := suite.CreateUserOnChainB(ctx, testvalues.StartingTokenAmount)
	receiverWalletAddress := receiverWallet.Bech32Address(chainB.Config().Bech32Prefix)

	t.Run("start relayer", func(t *testing.T) {
		suite.StartRelayer(relayer)
	})

	// createMsgGrantFn initializes a TransferAuthorization and broadcasts a MsgGrant message.
	createMsgGrantFn := func(t *testing.T) {
		transferAuth := transfertypes.TransferAuthorization{
			Allocations: []transfertypes.Allocation{
				{
					SourcePort:    channelA.PortID,
					SourceChannel: channelA.ChannelID,
					SpendLimit:    sdk.NewCoins(sdk.NewCoin(chainADenom, sdk.NewInt(testvalues.StartingTokenAmount))),
					AllowList:     []string{receiverWalletAddress},
				},
			},
		}

		authAny, err := codectypes.NewAnyWithValue(&transferAuth)
		suite.Require().NoError(err)

		msgGrant := &authz.MsgGrant{
			Granter: granterAddress,
			Grantee: granteeAddress,
			Grant: authz.Grant{
				Authorization: authAny,
				// no expiration
				Expiration: nil,
			},
		}

		resp, err := suite.BroadcastMessages(context.TODO(), chainA, granterWallet, msgGrant)
		suite.AssertValidTxResponse(resp)
		suite.Require().NoError(err)
	}

	// verifyGrantFn returns a test function which asserts chainA has a grant authorization
	// with the given spend limit.
	verifyGrantFn := func(expectedLimit int64) func(t *testing.T) {
		return func(t *testing.T) {
			grantAuths, err := suite.QueryGranterGrants(ctx, chainA, granterAddress)

			suite.Require().NoError(err)
			suite.Require().Len(grantAuths, 1)
			grantAuthorization := grantAuths[0]

			transferAuth := suite.extractTransferAuthorizationFromGrantAuthorization(grantAuthorization)
			expectedSpendLimit := sdk.NewCoins(sdk.NewCoin(chainADenom, sdk.NewInt(expectedLimit)))
			suite.Require().Equal(expectedSpendLimit, transferAuth.Allocations[0].SpendLimit)
		}
	}

	t.Run("broadcast MsgGrant", createMsgGrantFn)

	t.Run("broadcast MsgExec for ibc MsgTransfer", func(t *testing.T) {
		transferMsg := transfertypes.MsgTransfer{
			SourcePort:    channelA.PortID,
			SourceChannel: channelA.ChannelID,
			Token:         testvalues.DefaultTransferAmount(chainADenom),
			Sender:        granterAddress,
			Receiver:      receiverWalletAddress,
			TimeoutHeight: suite.GetTimeoutHeight(ctx, chainB),
		}

		transferAny, err := codectypes.NewAnyWithValue(&transferMsg)
		suite.Require().NoError(err)

		msgExec := &authz.MsgExec{
			Grantee: granteeAddress,
			Msgs:    []*codectypes.Any{transferAny},
		}

		resp, err := suite.BroadcastMessages(context.TODO(), chainA, granteeWallet, msgExec)
		suite.AssertValidTxResponse(resp)
		suite.Require().NoError(err)
	})

	t.Run("verify granter wallet amount", func(t *testing.T) {
		actualBalance, err := suite.GetChainANativeBalance(ctx, granterWallet)
		suite.Require().NoError(err)

		expected := testvalues.StartingTokenAmount - testvalues.IBCTransferAmount
		suite.Require().Equal(expected, actualBalance)
	})

	suite.Require().NoError(test.WaitForBlocks(context.TODO(), 10, chainB))

	t.Run("verify receiver wallet amount", func(t *testing.T) {
		chainBIBCToken := testsuite.GetIBCToken(chainADenom, channelA.Counterparty.PortID, channelA.Counterparty.ChannelID)
		actualBalance, err := chainB.GetBalance(ctx, receiverWalletAddress, chainBIBCToken.IBCDenom())
		suite.Require().NoError(err)
		suite.Require().Equal(testvalues.IBCTransferAmount, actualBalance)
	})

	t.Run("granter grant spend limit reduced", verifyGrantFn(testvalues.StartingTokenAmount-testvalues.IBCTransferAmount))

	t.Run("re-initialize MsgGrant", createMsgGrantFn)

	t.Run("granter grant was reinitialized", verifyGrantFn(testvalues.StartingTokenAmount))

}

// extractTransferAuthorizationFromGrantAuthorization extracts a TransferAuthorization from the given
// GrantAuthorization.
func (suite *AuthzTransferTestSuite) extractTransferAuthorizationFromGrantAuthorization(grantAuth *authz.GrantAuthorization) *transfertypes.TransferAuthorization {
	cfg := testsuite.EncodingConfig()
	var authorization authz.Authorization
	err := cfg.InterfaceRegistry.UnpackAny(grantAuth.Authorization, &authorization)
	suite.Require().NoError(err)

	transferAuth, ok := authorization.(*transfertypes.TransferAuthorization)
	suite.Require().True(ok)
	return transferAuth
}