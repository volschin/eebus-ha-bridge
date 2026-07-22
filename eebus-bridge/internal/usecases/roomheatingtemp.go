package usecases

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrRoomHeatingDataUnavailable = errors.New("room heating setpoint data unavailable")
	ErrRoomHeatingNotWritable     = errors.New("room heating setpoint is not writable")
	ErrRoomHeatingOutOfRange      = errors.New("room heating setpoint is outside the advertised range")
	ErrRoomHeatingInvalidStep     = errors.New("room heating setpoint does not match the advertised step")
	ErrRoomHeatingRejected        = errors.New("room heating setpoint write rejected by device")
)

// RoomHeatingSetpoint is the stable bridge contract populated by CRHT.
type RoomHeatingSetpoint struct {
	Value    float64
	Minimum  float64
	Maximum  float64
	Step     float64
	Writable bool
}

func validateRoomHeatingSetpointWrite(state RoomHeatingSetpoint, value float64) error {
	if !state.Writable {
		return ErrRoomHeatingNotWritable
	}
	if !isFinite(value) || value < state.Minimum || value > state.Maximum {
		return fmt.Errorf("%w: %.3f not in [%.3f, %.3f]", ErrRoomHeatingOutOfRange, value, state.Minimum, state.Maximum)
	}
	steps := math.Round((value - state.Minimum) / state.Step)
	if math.Abs(state.Minimum+steps*state.Step-value) > 1e-6 {
		return fmt.Errorf("%w: %.3f with step %.3f", ErrRoomHeatingInvalidStep, value, state.Step)
	}
	return nil
}
