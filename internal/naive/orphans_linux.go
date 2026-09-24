//go:build linux

package naive

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

func killOrphanedProcesses() {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		parts := strings.Split(string(cmdline), "\x00")
		if len(parts) < 2 || !strings.Contains(filepath.Base(parts[0]), "caddy") || !strings.Contains(parts[1], filepath.Join(config.GetBinFolderPath(), "naive")) {
			continue
		}
		_ = (&os.Process{Pid: pid}).Kill()
	}
}
