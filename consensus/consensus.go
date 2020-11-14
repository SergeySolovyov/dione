package consensus

import (
	"sync"

	"github.com/Secured-Finance/dione/models"
	"github.com/Secured-Finance/dione/pb"
	"github.com/sirupsen/logrus"
)

type ConsensusState int

const (
	consensusPrePrepared ConsensusState = 0x0
	consensusPrepared    ConsensusState = 0x1
	consensusCommitted   ConsensusState = 0x2

	testValidData = "test"
)

type PBFTConsensusManager struct {
	psb           *pb.PubSubRouter
	Consensuses   map[string]*ConsensusData
	maxFaultNodes int
	miner         *Miner
}

type ConsensusData struct {
	preparedCount             int
	commitCount               int
	State                     ConsensusState
	mutex                     sync.Mutex
	result                    string
	test                      bool
	onConsensusFinishCallback func(finalData string)
}

func NewPBFTConsensusManager(psb *pb.PubSubRouter, maxFaultNodes int) *PBFTConsensusManager {
	pcm := &PBFTConsensusManager{}
	pcm.Consensuses = make(map[string]*ConsensusData)
	pcm.psb = psb
	pcm.psb.Hook("prepared", pcm.handlePreparedMessage)
	pcm.psb.Hook("commit", pcm.handleCommitMessage)
	return pcm
}

func (pcm *PBFTConsensusManager) NewTestConsensus(data string, consensusID string, onConsensusFinishCallback func(finalData string)) {
	//consensusID := uuid.New().String()
	cData := &ConsensusData{}
	cData.test = true
	cData.onConsensusFinishCallback = onConsensusFinishCallback
	pcm.Consensuses[consensusID] = cData

	// here we will create DioneTask

	msg := models.Message{}
	msg.Type = "prepared"
	msg.Payload = make(map[string]interface{})
	msg.Payload["consensusID"] = consensusID
	msg.Payload["data"] = data
	pcm.psb.BroadcastToServiceTopic(&msg)

	cData.State = consensusPrePrepared
	logrus.Debug("started new consensus: " + consensusID)
}

func (pcm *PBFTConsensusManager) handlePreparedMessage(message *models.Message) {
	// TODO add check on view of the message
	consensusID := message.Payload["consensusID"].(string)
	if _, ok := pcm.Consensuses[consensusID]; !ok {
		logrus.Warn("Unknown consensus ID: " + consensusID)
		return
	}
	logrus.Debug("received prepared msg")
	data := pcm.Consensuses[consensusID]

	//// validate payload data
	//if data.test {
	//	rData := message.Payload["data"].(string)
	//	if rData != testValidData {
	//		logrus.Error("Incorrect data was received! Ignoring this message, because it was sent from fault node!")
	//		return
	//	}
	//} else {
	//	// TODO
	//}

	// here we can validate miner which produced this task, is he winner, and so on
	// we must to reconstruct transaction here for validating task itself

	data.mutex.Lock()
	data.preparedCount++
	data.mutex.Unlock()

	if data.preparedCount > 2*pcm.maxFaultNodes+1 {
		msg := models.Message{}
		msg.Payload = make(map[string]interface{})
		msg.Type = "commit"
		msg.Payload["consensusID"] = consensusID
		msg.Payload["data"] = message.Payload["data"]
		err := pcm.psb.BroadcastToServiceTopic(&msg)
		if err != nil {
			logrus.Warn("Unable to send COMMIT message: " + err.Error())
			return
		}
		data.State = consensusPrepared
	}
}

func (pcm *PBFTConsensusManager) handleCommitMessage(message *models.Message) {
	// TODO add check on view of the message
	// TODO add validation of data by hash to this stage
	consensusID := message.Payload["consensusID"].(string)
	if _, ok := pcm.Consensuses[consensusID]; !ok {
		logrus.Warn("Unknown consensus ID: " + consensusID)
		return
	}
	data := pcm.Consensuses[consensusID]

	data.mutex.Lock()
	defer data.mutex.Unlock()
	if data.State == consensusCommitted {
		logrus.Debug("consensus already finished, dropping COMMIT message")
		return
	}

	logrus.Debug("received commit msg")

	data.commitCount++

	if data.commitCount > 2*pcm.maxFaultNodes+1 {
		data.State = consensusCommitted
		data.result = message.Payload["data"].(string)
		logrus.Debug("consensus successfully finished with result: " + data.result)
		data.onConsensusFinishCallback(data.result)
	}
}
