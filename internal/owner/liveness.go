package owner

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// NewRunningProbe reads the process table once and returns a liveness probe
// for the scan. An Owner is running when a process with its display name, or an
// executable inside an app bundle named after it, is present. The probe never
// uses sudo and never asks for extra permissions.
func NewRunningProbe() func(Owner) bool {
	processes := processCommands()
	return func(o Owner) bool {
		return IsRunning(o, processes)
	}
}

// IsRunning reports whether any process command belongs to o. It is exported
// separately so the matching rule can be table-tested without a live process.
func IsRunning(o Owner, processes []string) bool {
	var names []string
	for _, n := range []string{o.Name, o.ID} {
		if n == "" {
			continue
		}
		n = strings.ToLower(n)
		if !contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return false
	}

	for _, raw := range processes {
		command := strings.TrimSpace(raw)
		if command == "" {
			continue
		}
		lower := strings.ToLower(command)
		base := strings.ToLower(filepath.Base(command))
		for _, name := range names {
			if base == name {
				return true
			}
			if strings.Contains(lower, "/"+name+".app/") {
				return true
			}
		}
	}
	return false
}

func processCommands() []string {
	out, err := exec.Command("/bin/ps", "-axo", "comm=").Output()
	if err != nil {
		return nil
	}
	return strings.Split(string(out), "\n")
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
