package statistics

import (
	"errors"
	"math/big"
	"strings"
	"time"
)

type DeltaStatus string

const (
	DeltaValid          DeltaStatus = "valid"
	DeltaUnchanged      DeltaStatus = "unchanged"
	DeltaNegativeReset  DeltaStatus = "negative_reset"
	DeltaTimeRegression DeltaStatus = "time_regression"
	DeltaInvalid        DeltaStatus = "invalid"
)

type Delta struct {
	Value  string      `json:"value,omitempty"`
	Status DeltaStatus `json:"status"`
}

func CalculateDelta(
	previousKWh, currentKWh string,
	previousTime, currentTime time.Time,
) (Delta, error) {
	if currentTime.Before(previousTime) {
		return Delta{Status: DeltaTimeRegression}, nil
	}
	previous, ok := new(big.Rat).SetString(strings.TrimSpace(previousKWh))
	if !ok {
		return Delta{Status: DeltaInvalid}, errors.New("invalid previous kWh")
	}
	current, ok := new(big.Rat).SetString(strings.TrimSpace(currentKWh))
	if !ok {
		return Delta{Status: DeltaInvalid}, errors.New("invalid current kWh")
	}
	difference := new(big.Rat).Sub(current, previous)
	status := DeltaValid
	switch difference.Sign() {
	case -1:
		status = DeltaNegativeReset
	case 0:
		status = DeltaUnchanged
	}
	return Delta{Value: difference.FloatString(4), Status: status}, nil
}
