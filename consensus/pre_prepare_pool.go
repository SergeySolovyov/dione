package consensus

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/Secured-Finance/dione/cache"

	"github.com/Secured-Finance/dione/consensus/validation"
	_ "github.com/Secured-Finance/dione/consensus/validation/filecoin" // enable filecoin validation
	"github.com/filecoin-project/go-state-types/crypto"

	types2 "github.com/Secured-Finance/dione/consensus/types"
	"github.com/Secured-Finance/dione/sigs"
	"github.com/Secured-Finance/dione/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/sirupsen/logrus"
)

type PrePreparePool struct {
	mut            sync.RWMutex
	prePrepareMsgs map[string][]*types2.Message
	miner          *Miner
	eventLogCache  *cache.EventLogCache
}

func NewPrePreparePool(miner *Miner, evc *cache.EventLogCache) *PrePreparePool {
	return &PrePreparePool{
		prePrepareMsgs: map[string][]*types2.Message{},
		miner:          miner,
		eventLogCache:  evc,
	}
}

func (pp *PrePreparePool) CreatePrePrepare(consensusID string, task types.DioneTask, requestID string, callbackAddress, callbackMethodID, privateKey []byte) (*types2.Message, error) {
	var message types2.Message
	message.Type = types2.MessageTypePrePrepare
	var consensusMsg types2.ConsensusMessage
	consensusMsg.ConsensusID = consensusID
	consensusMsg.RequestID = requestID
	consensusMsg.CallbackAddress = callbackAddress
	consensusMsg.CallbackMethodID = callbackMethodID
	consensusMsg.Task = task
	cHash, err := hashstructure.Hash(consensusMsg, hashstructure.FormatV2, nil)
	if err != nil {
		return nil, err
	}
	signature, err := sigs.Sign(types.SigTypeEd25519, privateKey, []byte(fmt.Sprintf("%v", cHash)))
	if err != nil {
		return nil, err
	}
	consensusMsg.Signature = signature.Data
	message.Payload = consensusMsg
	return &message, nil
}

func (ppp *PrePreparePool) IsExistingPrePrepare(prepareMsg *types2.Message) bool {
	ppp.mut.RLock()
	defer ppp.mut.RUnlock()

	consensusMessage := prepareMsg.Payload
	var exists bool
	for _, v := range ppp.prePrepareMsgs[consensusMessage.ConsensusID] {
		if v.From == prepareMsg.From {
			exists = true
		}
	}
	return exists
}

func (ppp *PrePreparePool) IsValidPrePrepare(prePrepare *types2.Message) bool {
	// TODO here we need to do validation of tx itself
	consensusMsg := prePrepare.Payload

	// === verify task signature ===
	err := verifyTaskSignature(consensusMsg)
	if err != nil {
		logrus.Errorf("unable to verify signature: %v", err)
		return false
	}
	/////////////////////////////////

	// === verify if request exists in event log cache ===
	requestEvent, err := ppp.eventLogCache.GetOracleRequestEvent("request_" + consensusMsg.RequestID)
	if err != nil {
		logrus.Errorf("the incoming request task event doesn't exist in the EVC, or is broken: %v", err)
		return false
	}
	if bytes.Compare(requestEvent.CallbackAddress.Bytes(), consensusMsg.CallbackAddress) != 0 ||
		bytes.Compare(requestEvent.CallbackMethodID[:], consensusMsg.CallbackMethodID) != 0 ||
		requestEvent.OriginChain != consensusMsg.Task.OriginChain ||
		requestEvent.RequestType != consensusMsg.Task.RequestType ||
		requestEvent.RequestParams != consensusMsg.Task.RequestParams {

		logrus.Errorf("the incoming task and cached request event don't match!")
		return false
	}
	/////////////////////////////////

	// === verify election proof wincount preliminarily ===
	if consensusMsg.Task.ElectionProof.WinCount < 1 {
		logrus.Error("miner isn't a winner!")
		return false
	}
	/////////////////////////////////

	// === verify miner's eligibility to propose this task ===
	err = ppp.miner.IsMinerEligibleToProposeTask(common.HexToAddress(consensusMsg.Task.MinerEth))
	if err != nil {
		logrus.Errorf("miner is not eligible to propose task: %v", err)
		return false
	}
	/////////////////////////////////

	// === verify election proof vrf ===
	minerAddressMarshalled, err := prePrepare.Payload.Task.Miner.MarshalBinary()
	if err != nil {
		logrus.Errorf("failed to marshal miner address: %v", err)
		return false
	}
	electionProofRandomness, err := DrawRandomness(
		consensusMsg.Task.BeaconEntries[1].Data,
		crypto.DomainSeparationTag_ElectionProofProduction,
		consensusMsg.Task.DrandRound,
		minerAddressMarshalled,
	)
	if err != nil {
		logrus.Errorf("failed to draw electionProofRandomness: %v", err)
		return false
	}
	err = VerifyVRF(consensusMsg.Task.Miner, electionProofRandomness, consensusMsg.Task.ElectionProof.VRFProof)
	if err != nil {
		logrus.Errorf("failed to verify election proof vrf: %v", err)
	}
	//////////////////////////////////////

	// === verify ticket vrf ===
	ticketRandomness, err := DrawRandomness(
		consensusMsg.Task.BeaconEntries[1].Data,
		crypto.DomainSeparationTag_TicketProduction,
		consensusMsg.Task.DrandRound-types.TicketRandomnessLookback,
		minerAddressMarshalled,
	)
	if err != nil {
		logrus.Errorf("failed to draw ticket electionProofRandomness: %v", err)
		return false
	}

	err = VerifyVRF(consensusMsg.Task.Miner, ticketRandomness, consensusMsg.Task.Ticket.VRFProof)
	if err != nil {
		logrus.Errorf("failed to verify ticket vrf: %v", err)
	}
	//////////////////////////////////////

	// === compute wincount locally and verify values ===
	mStake, nStake, err := ppp.miner.GetStakeInfo(common.HexToAddress(consensusMsg.Task.MinerEth))
	if err != nil {
		logrus.Errorf("failed to get miner stake: %v", err)
		return false
	}
	actualWinCount := consensusMsg.Task.ElectionProof.ComputeWinCount(*mStake, *nStake)
	if consensusMsg.Task.ElectionProof.WinCount != actualWinCount {
		logrus.Errorf("locally computed wincount isn't matching received value!", err)
		return false
	}
	//////////////////////////////////////

	// === validate payload by specific-chain checks ===
	if validationFunc := validation.GetValidationMethod(consensusMsg.Task.OriginChain, consensusMsg.Task.RequestType); validationFunc != nil {
		err := validationFunc(consensusMsg.Task.Payload)
		if err != nil {
			logrus.Errorf("payload validation has failed: %v", err)
		}
	} else {
		logrus.Warnf("Origin chain [%v]/request type[%v] doesn't have any payload validation!", consensusMsg.Task.OriginChain, consensusMsg.Task.RequestType)
	}
	/////////////////////////////////

	return true
}

func (ppp *PrePreparePool) AddPrePrepare(prePrepare *types2.Message) {
	ppp.mut.Lock()
	defer ppp.mut.Unlock()

	consensusID := prePrepare.Payload.ConsensusID
	if _, ok := ppp.prePrepareMsgs[consensusID]; !ok {
		ppp.prePrepareMsgs[consensusID] = []*types2.Message{}
	}

	ppp.prePrepareMsgs[consensusID] = append(ppp.prePrepareMsgs[consensusID], prePrepare)
}
