package base

// StatusCode is the code returned to the OS.
//
//go:generate stringer -type StatusCode -linecomment
type StatusCode uint8

// Status codes returned by the main executable.
const (
	SNoError           StatusCode = iota // No Error
	SGenericError                        // Generic Error
	SHelpRequested                       // Help Requested
	SInvalidParameters                   // Invalid Parameters
)
