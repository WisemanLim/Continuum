package cli

import (
	"fmt"
	"os/exec"
	"strconv"
)

// keychainSMB stores the SMB password as an internet password in the macOS login keychain (FR-S06),
// where Finder / `mount volume` find it.
func keychainSMB(host, port, user, password string) error {
	args := []string{"add-internet-password", "-U", "-a", user, "-s", host, "-r", "smb ", "-w", password, "-l", "Continuum " + host}
	if p, err := strconv.Atoi(port); err == nil {
		args = append(args, "-P", strconv.Itoa(p))
	}
	if out, err := exec.Command("security", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: %v %s", err, out)
	}
	return nil
}
