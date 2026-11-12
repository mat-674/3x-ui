package service

import (
	"context"

	"github.com/mhsanaei/3x-ui/v3/internal/edge"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// EdgeNginxInstaller returns an installer wired to this panel's settings, so a
// custom binary path is honoured instead of always resolving nginx from PATH.
func (s *SettingService) EdgeNginxInstaller() *edge.Installer {
	settings, err := s.GetEdgeSettings()
	if err != nil {
		logger.Warning("edge settings unreadable, installing nginx from PATH:", err)
		return edge.NewInstaller("")
	}
	return edge.NewInstaller(settings.NginxBin)
}

// InstallEdgeNginx installs the nginx binary on demand, which is what makes the
// perimeter usable without an install-time flag: enable it, and the panel offers
// to add what it needs.
func (s *SettingService) InstallEdgeNginx(ctx context.Context) (*edge.InstallReport, error) {
	report, err := s.EdgeNginxInstaller().Install(ctx)
	if err != nil {
		return report, err
	}
	logger.Infof("edge: nginx installed via %s at %s (%s)", report.PackageManager, report.Bin, report.Version)
	for _, step := range report.Steps {
		logger.Debugf("edge install: %s", step)
	}
	return report, nil
}
