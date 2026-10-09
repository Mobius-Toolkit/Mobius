package runner

import "golang.org/x/sys/unix"

// LoadAverage gives the 1-minute load average of the machine.
func LoadAverage() (float64, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0, err
	}
	return float64(info.Loads[0]) / (1 << 16), nil
}
