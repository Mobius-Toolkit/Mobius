package runner

import (
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// LoadAverage gives the 1-minute load average of the machine.
func LoadAverage() (float64, error) {
	// vm.loadavg is struct loadavg: three uint32 averages, 4 bytes of padding, and the uint64 scale.
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, err
	}
	if len(raw) < 24 {
		return 0, errors.New("vm.loadavg is too short")
	}
	scale := binary.NativeEndian.Uint64(raw[16:])
	if scale == 0 {
		return 0, errors.New("vm.loadavg has a scale of zero")
	}
	return float64(binary.NativeEndian.Uint32(raw)) / float64(scale), nil
}
