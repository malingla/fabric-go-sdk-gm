/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mocks

import (
	"github.com/hyperledger/fabric-sdk-go/pkg/common/context"
	"github.com/hyperledger/fabric-sdk-go/pkg/context/api/fab"
	"github.com/hyperledger/fabric-sdk-go/pkg/fab/txn"
	"github.com/pkg/errors"
)

// MockTransactor provides an implementation of Transactor that exposes all its context.
type MockTransactor struct {
	Ctx       context.Client
	ChannelID string
	Orderers  []fab.Orderer
}

// CreateTransactionHeader creates a Transaction Header based on the current context.
func (t *MockTransactor) CreateTransactionHeader() (fab.TransactionHeader, error) {
	txh, err := txn.NewHeader(t.Ctx, t.ChannelID)
	if err != nil {
		return nil, errors.WithMessage(err, "new transaction ID failed")
	}

	return txh, nil
}

// SendTransactionProposal sends a TransactionProposal to the target peers.
func (t *MockTransactor) SendTransactionProposal(proposal *fab.TransactionProposal, targets []fab.ProposalProcessor) ([]*fab.TransactionProposalResponse, error) {
	return txn.SendProposal(t.Ctx, proposal, targets)
}

// CreateTransaction create a transaction with proposal response.
func (t *MockTransactor) CreateTransaction(request fab.TransactionRequest) (*fab.Transaction, error) {
	return txn.New(request)
}

// SendTransaction send a transaction to the chain’s orderer service (one or more orderer endpoints) for consensus and committing to the ledger.
func (t *MockTransactor) SendTransaction(tx *fab.Transaction) (*fab.TransactionResponse, error) {
	return txn.Send(t.Ctx, tx, t.Orderers)
}