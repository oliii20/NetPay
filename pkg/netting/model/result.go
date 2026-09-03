// Package model contains protocol records shared by netting components.
package model

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
)

var (
	ErrInvalidOriginalAmount = errors.New("invalid original intent amount")
	ErrInvalidMatchedAmount  = errors.New("invalid matched amount")
	ErrInvalidFallbackAmount = errors.New("invalid fallback amount")
	ErrIntentIDMismatch      = errors.New("intent ID mismatch")
	ErrAmountConservation    = errors.New("intent result does not conserve amount")
)

type IntentResult struct {
	Intent         intent.PaymentIntent
	IntentID       intent.ID
	MatchedAmount  *big.Int
	FallbackAmount *big.Int
}

func NewIntentResult(payment intent.PaymentIntent, matched *big.Int) (IntentResult, error) {
	if payment.Amount == nil || payment.Amount.Sign() <= 0 {
		return IntentResult{}, ErrInvalidOriginalAmount
	}
	if matched == nil || matched.Sign() < 0 || matched.Cmp(payment.Amount) > 0 {
		return IntentResult{}, ErrInvalidMatchedAmount
	}

	id, err := payment.ID()
	if err != nil {
		return IntentResult{}, fmt.Errorf("calculate intent ID: %w", err)
	}

	return IntentResult{
		Intent:         CloneIntent(payment),
		IntentID:       id,
		MatchedAmount:  new(big.Int).Set(matched),
		FallbackAmount: new(big.Int).Sub(payment.Amount, matched),
	}, nil
}

func (r IntentResult) Validate() error {
	if r.Intent.Amount == nil || r.Intent.Amount.Sign() <= 0 {
		return ErrInvalidOriginalAmount
	}
	if r.MatchedAmount == nil || r.MatchedAmount.Sign() < 0 || r.MatchedAmount.Cmp(r.Intent.Amount) > 0 {
		return ErrInvalidMatchedAmount
	}
	if r.FallbackAmount == nil || r.FallbackAmount.Sign() < 0 || r.FallbackAmount.Cmp(r.Intent.Amount) > 0 {
		return ErrInvalidFallbackAmount
	}

	id, err := r.Intent.ID()
	if err != nil {
		return fmt.Errorf("calculate intent ID: %w", err)
	}
	if id != r.IntentID {
		return ErrIntentIDMismatch
	}

	total := new(big.Int).Add(r.MatchedAmount, r.FallbackAmount)
	if total.Cmp(r.Intent.Amount) != 0 {
		return ErrAmountConservation
	}

	return nil
}

func CloneIntent(payment intent.PaymentIntent) intent.PaymentIntent {
	cloned := payment
	if payment.Amount != nil {
		cloned.Amount = new(big.Int).Set(payment.Amount)
	}
	cloned.Signature = bytes.Clone(payment.Signature)

	return cloned
}
