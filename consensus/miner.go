package consensus

import (
	"context"
	"sync"

	"github.com/Secured-Finance/dione/beacon"

	"github.com/libp2p/go-libp2p-core/peer"

	"github.com/Secured-Finance/dione/ethclient"
	"github.com/Secured-Finance/dione/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/sirupsen/logrus"
	"golang.org/x/xerrors"
)

type Miner struct {
	address      peer.ID
	ethAddress   common.Address
	api          WalletAPI
	mutex        sync.Mutex
	beacon       beacon.BeaconNetworks
	ethClient    *ethclient.EthereumClient
	minerStake   types.BigInt
	networkStake types.BigInt
}

func NewMiner(
	address peer.ID,
	ethAddress common.Address,
	api WalletAPI,
	beacon beacon.BeaconNetworks,
	ethClient *ethclient.EthereumClient,
) *Miner {
	return &Miner{
		address:    address,
		ethAddress: ethAddress,
		api:        api,
		beacon:     beacon,
		ethClient:  ethClient,
	}
}

type WalletAPI interface {
	WalletSign(context.Context, peer.ID, []byte) (*types.Signature, error)
}

func (m *Miner) UpdateCurrentStakeInfo() error {
	mStake, err := m.ethClient.GetMinerStake(m.ethAddress)

	if err != nil {
		logrus.Warn("Can't get miner stake", err)
		return err
	}

	nStake, err := m.ethClient.GetTotalStake()

	if err != nil {
		logrus.Warn("Can't get miner stake", err)
		return err
	}

	m.minerStake = *mStake
	m.networkStake = *nStake

	return nil
}

func (m *Miner) MineTask(ctx context.Context, payload []byte) (*types.DioneTask, error) {
	bvals, err := beacon.BeaconEntriesForTask(ctx, m.beacon)
	if err != nil {
		return nil, xerrors.Errorf("failed to get beacon entries: %w", err)
	}
	logrus.Debug("attempting to mine the task at epoch: ", bvals[1].Round)

	rbase := bvals[1]

	if err := m.UpdateCurrentStakeInfo(); err != nil {
		return nil, xerrors.Errorf("failed to update miner stake: %w", err)
	}

	ticket, err := m.computeTicket(ctx, &rbase)
	if err != nil {
		return nil, xerrors.Errorf("scratching ticket failed: %w", err)
	}

	winner, err := IsRoundWinner(ctx, types.DrandRound(rbase.Round), m.address, rbase, m.minerStake, m.networkStake, m.api)
	if err != nil {
		return nil, xerrors.Errorf("failed to check if we win next round: %w", err)
	}

	if winner == nil {
		return nil, nil
	}
	return &types.DioneTask{
		Miner:         m.address,
		Ticket:        ticket,
		ElectionProof: winner,
		BeaconEntries: bvals,
		Payload:       payload,
		// TODO: signature
		DrandRound: types.DrandRound(rbase.Round),
	}, nil
}

func (m *Miner) computeTicket(ctx context.Context, brand *types.BeaconEntry) (*types.Ticket, error) {
	buf, err := m.address.MarshalBinary()
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal address: %w", err)
	}

	round := types.DrandRound(brand.Round)

	input, err := DrawRandomness(brand.Data, crypto.DomainSeparationTag_TicketProduction, round-types.TicketRandomnessLookback, buf)
	if err != nil {
		return nil, err
	}

	vrfOut, err := ComputeVRF(ctx, m.api.WalletSign, m.address, input)
	if err != nil {
		return nil, err
	}

	return &types.Ticket{
		VRFProof: vrfOut,
	}, nil
}
