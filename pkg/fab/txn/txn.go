/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package txn enables creating, endorsing and sending transactions to Fabric peers and orderers.
package txn

import (
	"bytes"
	reqContext "context"
	"math/rand"

	"github.com/pkg/errors"

	contextApi "github.com/hyperledger/fabric-sdk-go/pkg/common/context"
	"github.com/hyperledger/fabric-sdk-go/pkg/context"
	"github.com/hyperledger/fabric-sdk-go/pkg/context/api/core"
	"github.com/hyperledger/fabric-sdk-go/pkg/context/api/fab"
	"github.com/hyperledger/fabric-sdk-go/pkg/logging"
	"github.com/hyperledger/fabric-sdk-go/third_party/github.com/hyperledger/fabric/protos/common"
	pb "github.com/hyperledger/fabric-sdk-go/third_party/github.com/hyperledger/fabric/protos/peer"
	protos_utils "github.com/hyperledger/fabric-sdk-go/third_party/github.com/hyperledger/fabric/protos/utils"
)

var logger = logging.NewLogger("fabsdk/fab")

// CCProposalType reflects transitions in the chaincode lifecycle
type CCProposalType int

// Define chaincode proposal types
const (
	Instantiate CCProposalType = iota
	Upgrade
)

// New create a transaction with proposal response, following the endorsement policy.
func New(request fab.TransactionRequest) (*fab.Transaction, error) {
	if len(request.ProposalResponses) == 0 {
		return nil, errors.New("at least one proposal response is necessary")
	}

	proposal := request.Proposal

	// the original header
	hdr, err := protos_utils.GetHeader(proposal.Header)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal proposal header failed")
	}

	// the original payload
	pPayl, err := protos_utils.GetChaincodeProposalPayload(proposal.Payload)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal proposal payload failed")
	}

	// get header extensions so we have the visibility field
	hdrExt, err := protos_utils.GetChaincodeHeaderExtension(hdr)
	if err != nil {
		return nil, err
	}

	responsePayload := request.ProposalResponses[0].ProposalResponse.Payload
	for _, r := range request.ProposalResponses {
		if r.ProposalResponse.Response.Status != 200 {
			return nil, errors.Errorf("proposal response was not successful, error code %d, msg %s", r.ProposalResponse.Response.Status, r.ProposalResponse.Response.Message)
		}
		if !bytes.Equal(responsePayload, r.ProposalResponse.Payload) {
			return nil, errors.Errorf("proposal response payloads are not the same (%v, %v)", responsePayload, r.ProposalResponse.Payload)
		}
	}

	// fill endorsements
	endorsements := make([]*pb.Endorsement, len(request.ProposalResponses))
	for n, r := range request.ProposalResponses {
		endorsements[n] = r.ProposalResponse.Endorsement
	}

	// create ChaincodeEndorsedAction
	cea := &pb.ChaincodeEndorsedAction{ProposalResponsePayload: responsePayload, Endorsements: endorsements}

	// obtain the bytes of the proposal payload that will go to the transaction
	propPayloadBytes, err := protos_utils.GetBytesProposalPayloadForTx(pPayl, hdrExt.PayloadVisibility)
	if err != nil {
		return nil, err
	}

	// serialize the chaincode action payload
	cap := &pb.ChaincodeActionPayload{ChaincodeProposalPayload: propPayloadBytes, Action: cea}
	capBytes, err := protos_utils.GetBytesChaincodeActionPayload(cap)
	if err != nil {
		return nil, err
	}

	// create a transaction
	taa := &pb.TransactionAction{Header: hdr.SignatureHeader, Payload: capBytes}
	taas := make([]*pb.TransactionAction, 1)
	taas[0] = taa

	return &fab.Transaction{
		Transaction: &pb.Transaction{Actions: taas},
		Proposal:    proposal,
	}, nil
}

// Send send a transaction to the chain’s orderer service (one or more orderer endpoints) for consensus and committing to the ledger.
func Send(ctx contextApi.Client, tx *fab.Transaction, orderers []fab.Orderer) (*fab.TransactionResponse, error) {
	if orderers == nil || len(orderers) == 0 {
		return nil, errors.New("orderers is nil")
	}
	if tx == nil {
		return nil, errors.New("transaction is nil")
	}
	if tx.Proposal == nil || tx.Proposal.Proposal == nil {
		return nil, errors.New("proposal is nil")
	}

	// the original header
	hdr, err := protos_utils.GetHeader(tx.Proposal.Proposal.Header)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal proposal header failed")
	}
	// serialize the tx
	txBytes, err := protos_utils.GetBytesTransaction(tx.Transaction)
	if err != nil {
		return nil, err
	}

	// create the payload
	payload := common.Payload{Header: hdr, Data: txBytes}

	transactionResponse, err := BroadcastPayload(ctx, &payload, orderers)
	if err != nil {
		return nil, err
	}

	return transactionResponse, nil
}

// BroadcastPayload will send the given payload to some orderer, picking random endpoints
// until all are exhausted
func BroadcastPayload(ctx contextApi.Client, payload *common.Payload, orderers []fab.Orderer) (*fab.TransactionResponse, error) {
	// Check if orderers are defined
	if len(orderers) == 0 {
		return nil, errors.New("orderers not set")
	}

	envelope, err := signPayload(ctx, payload)
	if err != nil {
		return nil, err
	}

	return broadcastEnvelope(ctx, envelope, orderers)
}

// broadcastEnvelope will send the given envelope to some orderer, picking random endpoints
// until all are exhausted
func broadcastEnvelope(ctx contextApi.Client, envelope *fab.SignedEnvelope, orderers []fab.Orderer) (*fab.TransactionResponse, error) {
	// Check if orderers are defined
	if len(orderers) == 0 {
		return nil, errors.New("orderers not set")
	}

	// Copy aside the ordering service endpoints
	randOrderers := []fab.Orderer{}
	for _, o := range orderers {
		randOrderers = append(randOrderers, o)
	}

	// Iterate them in a random order and try broadcasting 1 by 1
	var errResp error
	for _, i := range rand.Perm(len(randOrderers)) {
		resp, err := sendBroadcast(ctx, envelope, randOrderers[i])
		if err != nil {
			errResp = err
		} else {
			return resp, nil
		}
	}
	return nil, errResp
}

func sendBroadcast(ctx contextApi.Client, envelope *fab.SignedEnvelope, orderer fab.Orderer) (*fab.TransactionResponse, error) {
	logger.Debugf("Broadcasting envelope to orderer :%s\n", orderer.URL())
	reqCtx, cancel := reqContext.WithTimeout(context.NewRequest(ctx), ctx.Config().TimeoutOrDefault(core.OrdererResponse))
	defer cancel()
	if _, err := orderer.SendBroadcast(reqCtx, envelope); err != nil {
		logger.Debugf("Receive Error Response from orderer :%v\n", err)
		return nil, errors.Wrapf(err, "calling orderer '%s' failed", orderer.URL())
	}

	logger.Debugf("Receive Success Response from orderer\n")
	return &fab.TransactionResponse{Orderer: orderer.URL()}, nil
}

// SendPayload sends the given payload to each orderer and returns a block response
func SendPayload(ctx contextApi.Client, payload *common.Payload, orderers []fab.Orderer) (*common.Block, error) {
	if orderers == nil || len(orderers) == 0 {
		return nil, errors.New("orderers not set")
	}

	envelope, err := signPayload(ctx, payload)
	if err != nil {
		return nil, err
	}

	// Copy aside the ordering service endpoints
	randOrderers := []fab.Orderer{}
	for _, o := range orderers {
		randOrderers = append(randOrderers, o)
	}

	// Iterate them in a random order and try broadcasting 1 by 1
	var errResp error
	for _, i := range rand.Perm(len(randOrderers)) {
		resp, err := sendEnvelope(ctx, envelope, randOrderers[i])
		if err != nil {
			errResp = err
		} else {
			return resp, nil
		}
	}
	return nil, errResp
}

// sendEnvelope sends the given envelope to each orderer and returns a block response
func sendEnvelope(ctx contextApi.Client, envelope *fab.SignedEnvelope, orderer fab.Orderer) (*common.Block, error) {

	logger.Debugf("Broadcasting envelope to orderer :%s\n", orderer.URL())
	reqCtx, cancel := reqContext.WithTimeout(context.NewRequest(ctx), ctx.Config().TimeoutOrDefault(core.OrdererResponse))
	defer cancel()
	blocks, errs := orderer.SendDeliver(reqCtx, envelope)

	var block *common.Block
	for {
		select {
		case b, ok := <-blocks:
			// We need to block until SendDeliver releases the connection. Currently
			// this is trigged by the go chan closing.
			// TODO: we may want to refactor (e.g., adding a synchronous SendDeliver)
			if !ok {
				return block, nil
			}
			block = b
		case err := <-errs:
			return nil, errors.Wrapf(err, "error from orderer")
		}
	}
}