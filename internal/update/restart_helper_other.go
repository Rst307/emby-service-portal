//go:build !windows

package update

import (
	"fmt"
	"os"
	"path/filepath"
)

// installPlatform swaps the running binary: rename the current executable aside,
// move the downloaded one into place, then let the caller exit with
// RestartExitCode so the service manager relaunches the new binary.
func installPlatform(_ *Service, dir, base, downloaded string) (bool, error) {
	executable := filepath.Join(dir, base)
	backup := filepath.Join(dir, base+".old")
	_ = os.Remove(backup)
	if err := os.Rename(executable, backup); err != nil {
		return false, fmt.Errorf("备份当前程序失败: %w", err)
	}
	if err := os.Rename(downloaded, executable); err != nil {
		_ = os.Rename(backup, executable)
		return false, fmt.Errorf("替换程序失败: %w", err)
	}
	_ = os.Chmod(executable, 0o755)
	return true, nil
}
