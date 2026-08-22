//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// installPlatform cannot replace the running executable (Windows locks it), so
// it lays the downloaded binary next to it, writes a detached helper script
// that waits for this process to exit, swaps the file and relaunches the new
// binary, and starts that script detached. The caller then exits with
// RestartExitCode; this process ending is what unlocks the file.
func installPlatform(_ *Service, dir, base string, downloaded string) (bool, error) {
	scriptPath := filepath.Join(dir, base+".update.bat")
	script := restartScriptWindows(os.Getpid(), dir, base)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return false, fmt.Errorf("无法写入重启脚本: %w", err)
	}
	cmd := exec.Command("cmd", "/c", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP: no console window,
		// independent of this process's lifetime.
		CreationFlags: 0x00000008 | 0x00000200,
	}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(scriptPath)
		return false, fmt.Errorf("无法启动更新助手: %w", err)
	}
	_ = cmd.Process.Release()
	_ = downloaded // the .bat moves base.update.tmp into place after we exit
	return true, nil
}

// restartScriptWindows returns the batch helper that waits for the old
// process, swaps the downloaded binary in and relaunches it.
func restartScriptWindows(pid int, dir, base string) string {
	return "@echo off\r\n" +
		"rem Emby Service Portal self-update helper.\r\n" +
		"rem Waits for the old process to exit, swaps the new binary into\r\n" +
		"rem place and relaunches it, then deletes itself.\r\n" +
		"set \"OLD_PID=" + fmt.Sprint(pid) + "\"\r\n" +
		"set \"TARGET_DIR=" + dir + "\"\r\n" +
		"set \"BINARY=" + base + "\"\r\n" +
		":wait\r\n" +
		"tasklist /FI \"PID eq %OLD_PID%\" | find \"%OLD_PID%\" >nul\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  timeout /t 1 /nobreak >nul\r\n" +
		"  goto wait\r\n" +
		")\r\n" +
		":swap\r\n" +
		"move /Y \"%TARGET_DIR%\\%BINARY%.update.tmp\" \"%TARGET_DIR%\\%BINARY%\" >nul 2>&1\r\n" +
		"if errorlevel 1 (\r\n" +
		"  timeout /t 2 /nobreak >nul\r\n" +
		"  goto swap\r\n" +
		")\r\n" +
		"start \"\" /b \"%TARGET_DIR%\\%BINARY%\"\r\n" +
		"del \"%~f0\" >nul 2>&1\r\n"
}
