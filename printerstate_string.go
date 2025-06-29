// Code generated by "stringer -type=printerState -trimprefix=state"; DO NOT EDIT.

package thermoprint

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[stateIdle-0]
	_ = x[stateInitializing-1]
	_ = x[stateReady-2]
	_ = x[statePrinting-3]
	_ = x[statePaused-4]
	_ = x[stateWaitingRetry-5]
	_ = x[stateCompleted-6]
	_ = x[stateFailed-7]
}

const _printerState_name = "IdleInitializingReadyPrintingPausedWaitingRetryCompletedFailed"

var _printerState_index = [...]uint8{0, 4, 16, 21, 29, 35, 47, 56, 62}

func (i printerState) String() string {
	if i < 0 || i >= printerState(len(_printerState_index)-1) {
		return "printerState(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _printerState_name[_printerState_index[i]:_printerState_index[i+1]]
}
