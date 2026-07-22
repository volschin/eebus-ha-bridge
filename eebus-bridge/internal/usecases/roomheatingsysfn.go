package usecases

import "errors"

var (
	ErrRoomHeatingSysFnDataUnavailable = errors.New("room heating system function data unavailable")
	ErrRoomHeatingSysFnNotWritable     = errors.New("room heating system function is not writable")
	ErrRoomHeatingSysFnInvalidMode     = errors.New("room heating operation mode is not advertised")
	ErrRoomHeatingSysFnRejected        = errors.New("room heating system function write rejected by device")
)

// RoomHeatingSystemFunctionState is the stable bridge contract composed from
// MRHSF state and CRHSF write capability.
type RoomHeatingSystemFunctionState struct {
	OperationMode  string
	AvailableModes []string
	ModeWritable   bool
}
