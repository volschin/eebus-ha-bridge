package usecases

import "errors"

var (
	ErrDHWSysFnDataUnavailable = errors.New("DHW system function data unavailable")
	ErrDHWSysFnNotWritable     = errors.New("DHW system function is not writable")
	ErrDHWSysFnInvalidMode     = errors.New("DHW operation mode is not advertised")
	ErrDHWSysFnRejected        = errors.New("DHW system function write rejected by device")
)

// DHWSystemFunctionState is the bridge-domain view composed from eebus-go's
// MDSF read state and CDSF write capabilities.
type DHWSystemFunctionState struct {
	BoostStatus    string
	BoostWritable  bool
	OperationMode  string
	AvailableModes []string
	ModeWritable   bool
}
