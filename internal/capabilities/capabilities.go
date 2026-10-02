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

type Disabled struct{}

func (Disabled) SendCode(context.Context, string, string) error { return ErrDisabled }
func (Disabled) CreateIntent(context.Context, string, int64, string) (string, error) {
	return "", ErrDisabled
}
func (Disabled) Request(context.Context, string, int64) (string, error) { return "", ErrDisabled }
func (Disabled) Draw(context.Context, string, string) (string, error)   { return "", ErrDisabled }

func Status() map[string]map[string]string {
	return map[string]map[string]string{
		"sms":         {"status": "disabled", "phase": "planned"},
		"payments":    {"status": "disabled", "phase": "planned"},
		"withdrawals": {"status": "disabled", "phase": "planned"},
		"lottery":     {"status": "disabled", "phase": "planned"},
	}
}

var _ SMS = Disabled{}
var _ Payment = Disabled{}
var _ Withdrawal = Disabled{}
var _ Lottery = Disabled{}
