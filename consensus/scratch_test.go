package consensus

import (
	"math"
	"testing"
	"time"

	"go.sia.tech/core/types"

	"lukechampine.com/frand"
)

// copied from testutil (can't import due to cycle)
func findBlockNonce(h *types.BlockHeader, target types.BlockID) {
	h.Nonce = frand.Uint64n(math.MaxUint32) * NonceFactor
	for !h.ID().MeetsTarget(target) {
		h.Nonce += NonceFactor
	}
}

func mineBlock(vc ValidationContext, parent types.Block, txns ...types.Transaction) types.Block {
	b := types.Block{
		Header: types.BlockHeader{
			Height:    parent.Header.Height + 1,
			ParentID:  parent.Header.ID(),
			Timestamp: parent.Header.Timestamp.Add(time.Second),
		},
		Transactions: txns,
	}
	b.Header.Commitment = vc.Commitment(b.Header.MinerAddress, b.Transactions)
	findBlockNonce(&b.Header, types.HashRequiringWork(vc.Difficulty))
	return b
}

func TestScratchChain(t *testing.T) {
	pubkey, privkey := testingKeypair(0)
	ourAddr := types.StandardAddress(pubkey)

	b := genesisWithSiacoinOutputs([]types.SiacoinOutput{
		{Value: types.Siacoins(1), Address: ourAddr},
		{Value: types.Siacoins(2), Address: ourAddr},
		{Value: types.Siacoins(3), Address: ourAddr},
		{Value: types.Siacoins(4), Address: ourAddr},
		{Value: types.Siacoins(5), Address: ourAddr},
		{Value: types.Siacoins(6), Address: ourAddr},
		{Value: types.Siacoins(7), Address: ourAddr},
		{Value: types.Siacoins(8), Address: ourAddr},
		{Value: types.Siacoins(9), Address: ourAddr},
		{Value: types.Siacoins(10), Address: ourAddr},
		{Value: types.Siacoins(11), Address: ourAddr},
		{Value: types.Siacoins(12), Address: ourAddr},
		{Value: types.Siacoins(13), Address: ourAddr},
	}...)
	sau := GenesisUpdate(b, testingDifficulty)

	sc := NewScratchChain(sau.Context)
	var blocks []types.Block
	origOutputs := sau.NewSiacoinElements
	toSpend := origOutputs[5:10]
	var spendTotal types.Currency
	for _, o := range toSpend {
		spendTotal = spendTotal.Add(o.Value)
	}
	txn := types.Transaction{
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:   spendTotal.Sub(types.Siacoins(1)),
			Address: ourAddr,
		}},
		MinerFee: types.Siacoins(1),
	}
	for _, o := range toSpend {
		txn.SiacoinInputs = append(txn.SiacoinInputs, types.SiacoinInput{
			Parent:      o,
			SpendPolicy: types.PolicyPublicKey(pubkey),
		})
	}
	signAllInputs(&txn, sau.Context, privkey)

	b = mineBlock(sau.Context, b, txn)
	if err := sc.AppendHeader(b.Header); err != nil {
		t.Fatal(err)
	}
	blocks = append(blocks, b)

	sau = ApplyBlock(sau.Context, b)
	sau.UpdateElementProof(&origOutputs[2].StateElement)
	newOutputs := sau.NewSiacoinElements

	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      newOutputs[1],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:   newOutputs[1].Value.Sub(types.Siacoins(1)),
			Address: ourAddr,
		}},
		MinerFee: types.Siacoins(1),
	}
	signAllInputs(&txn, sau.Context, privkey)

	b = mineBlock(sau.Context, b, txn)
	if err := sc.AppendHeader(b.Header); err != nil {
		t.Fatal(err)
	}
	blocks = append(blocks, b)
	sau = ApplyBlock(sau.Context, b)
	for i := range origOutputs {
		sau.UpdateElementProof(&origOutputs[i].StateElement)
	}
	toSpend = origOutputs[2:3]
	spendTotal = types.ZeroCurrency
	for _, o := range toSpend {
		spendTotal = spendTotal.Add(o.Value)
	}
	parentTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      toSpend[0],
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:   spendTotal,
			Address: ourAddr,
		}},
	}
	signAllInputs(&parentTxn, sau.Context, privkey)
	childTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent: types.SiacoinElement{
				StateElement: types.StateElement{
					ID: types.ElementID{
						Source: types.Hash256(parentTxn.ID()),
						Index:  0,
					},
					LeafIndex: types.EphemeralLeafIndex,
				},
				SiacoinOutput: types.SiacoinOutput{
					Value:   spendTotal,
					Address: ourAddr,
				},
			},
			SpendPolicy: types.PolicyPublicKey(pubkey),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:   spendTotal.Sub(types.Siacoins(1)),
			Address: ourAddr,
		}},
		MinerFee: types.Siacoins(1),
	}
	signAllInputs(&childTxn, sau.Context, privkey)

	b = mineBlock(sau.Context, b, parentTxn, childTxn)
	if err := sc.AppendHeader(b.Header); err != nil {
		t.Fatal(err)
	}
	blocks = append(blocks, b)

	// validate all blocks
	for _, b := range blocks {
		if _, err := sc.ApplyBlock(b); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScratchChainDifficultyAdjustment(t *testing.T) {
	b := genesisWithSiacoinOutputs()
	vc := GenesisUpdate(b, testingDifficulty).Context

	// mine a block, triggering adjustment
	sc := NewScratchChain(vc)
	b = mineBlock(vc, b)
	if err := sc.AppendHeader(b.Header); err != nil {
		t.Fatal(err)
	} else if _, err := sc.ApplyBlock(b); err != nil {
		t.Fatal(err)
	}
	vc = ApplyBlock(vc, b).Context

	// difficulty should have changed
	currentDifficulty := sc.tvc.Difficulty
	if currentDifficulty.Cmp(testingDifficulty) <= 0 {
		t.Fatal("difficulty should have increased")
	}

	// mine a block with less than the minimum work; it should be rejected
	b = mineBlock(vc, b)
	for types.WorkRequiredForHash(b.ID()).Cmp(currentDifficulty) >= 0 {
		b.Header.Nonce = frand.Uint64n(math.MaxUint32) * NonceFactor
	}
	if err := sc.AppendHeader(b.Header); err == nil {
		t.Fatal("expected block to be rejected")
	}

	// mine at actual difficulty
	findBlockNonce(&b.Header, types.HashRequiringWork(vc.Difficulty))
	if err := sc.AppendHeader(b.Header); err != nil {
		t.Fatal(err)
	} else if _, err := sc.ApplyBlock(b); err != nil {
		t.Fatal(err)
	}
	vc = ApplyBlock(vc, b).Context
}
