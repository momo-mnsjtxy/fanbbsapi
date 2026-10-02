// Package capabilities defines integration boundaries that are deliberately off.
// The local vertical slice never simulates money movement or external delivery.
package capabilities

import (
	"context"
	"errors"
)

var ErrDisabled = errors.New("capability disabled in local vertical slice")

type SMS interface {
	SendCode(context.Context, string, string) error
}

type Payment interface {
	CreateIntent(context.Context, string, int64, string) (string, error)
}

type Withdrawal interface {
	Request(context.Context, string, int64) (string, error)
}

type Lottery interface {
	Draw(context.Context, string, string) (string, error)
}

type ExternalFulfillment interface {
	CreateShipment(context.Context, string) (string, error)
}

type PaidContent interface {
	Purchase(context.Context, string, string) error
}

type CashWallet interface {
	Credit(context.Context, string, int64) error
}

type VIPPurchase interface {
	Activate(context.Context, string, string) error
}

type RaffleGambling interface {
	Enter(context.Context, string, string) (string, error)
}

type Disabled struct{}

func (Disabled) SendCode(context.Context, string, string) error { return ErrDisabled }
func (Disabled) CreateIntent(context.Context, string, int64, string) (string, error) {
	return "", ErrDisabled
}
func (Disabled) Request(context.Context, string, int64) (string, error) { return "", ErrDisabled }
func (Disabled) Draw(context.Context, string, string) (string, error)   { return "", ErrDisabled }
func (Disabled) CreateShipment(context.Context, string) (string, error) { return "", ErrDisabled }
func (Disabled) Purchase(context.Context, string, string) error         { return ErrDisabled }
func (Disabled) Credit(context.Context, string, int64) error            { return ErrDisabled }
func (Disabled) Activate(context.Context, string, string) error         { return ErrDisabled }
func (Disabled) Enter(context.Context, string, string) (string, error)  { return "", ErrDisabled }

func Status() map[string]map[string]string {
	return map[string]map[string]string{
		"sms":                  {"status": "disabled", "phase": "planned"},
		"payments":             {"status": "disabled", "phase": "planned"},
		"withdrawals":          {"status": "disabled", "phase": "planned"},
		"lottery":              {"status": "disabled", "phase": "planned"},
		"raffle_gambling":      {"status": "disabled", "phase": "not_planned"},
		"cash_wallet":          {"status": "disabled", "phase": "not_planned"},
		"vip_purchase":         {"status": "disabled", "phase": "planned"},
		"paid_content":         {"status": "disabled", "phase": "planned"},
		"external_fulfillment": {"status": "disabled", "phase": "planned"},
	}
}

var _ SMS = Disabled{}
var _ Payment = Disabled{}
var _ Withdrawal = Disabled{}
var _ Lottery = Disabled{}
var _ ExternalFulfillment = Disabled{}
var _ PaidContent = Disabled{}
var _ CashWallet = Disabled{}
var _ VIPPurchase = Disabled{}
var _ RaffleGambling = Disabled{}
