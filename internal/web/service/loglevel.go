package service

import (
	"strings"

	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// The log levels an operator can pick from the panel. The file backend always
// keeps DEBUG, so this only decides what also reaches the console/syslog: a
// debug toggle must not require restarting the panel to take effect.
const (
	// LogLevelDebug is the verbose level used while diagnosing a problem.
	LogLevelDebug   = "debug"
	LogLevelInfo    = "info"
	LogLevelNotice  = "notice"
	LogLevelWarning = "warning"
	LogLevelError   = "error"
)

// logLevels maps a setting value onto the backend level. debug is deliberately
// first: it is the value an operator reaches for, and everything unknown falls
// back to info rather than to a level that hides warnings.
var logLevels = map[string]logging.Level{
	LogLevelDebug:   logging.DEBUG,
	LogLevelInfo:    logging.INFO,
	LogLevelNotice:  logging.NOTICE,
	LogLevelWarning: logging.WARNING,
	LogLevelError:   logging.ERROR,
}

// NormalizeLogLevel validates a level and returns the canonical spelling.
func NormalizeLogLevel(level string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(level))
	if normalized == "" {
		return LogLevelInfo, nil
	}
	if _, ok := logLevels[normalized]; !ok {
		return "", common.NewErrorf("unknown log level %q: use debug, info, notice, warning or error", level)
	}
	return normalized, nil
}

// GetLogLevel returns the level currently configured in the panel.
func (s *SettingService) GetLogLevel() (string, error) {
	level, err := s.getString("logLevel")
	if err != nil {
		return LogLevelInfo, err
	}
	return NormalizeLogLevel(level)
}

// SetLogLevel stores a level and applies it to the running process, so the
// diagnostics toggle works without a full settings form or a restart.
func (s *SettingService) SetLogLevel(level string) (string, error) {
	normalized, err := NormalizeLogLevel(level)
	if err != nil {
		return "", err
	}
	if err := s.setString("logLevel", normalized); err != nil {
		return "", err
	}
	logger.InitLogger(logLevels[normalized])
	logger.Infof("log level set to %s from the diagnostics page", normalized)
	return normalized, nil
}

// ApplyLogLevel re-initialises the logger at the stored level, so switching to
// debug on the diagnostics page takes effect without a restart.
func (s *SettingService) ApplyLogLevel() error {
	level, err := s.GetLogLevel()
	if err != nil {
		return err
	}
	logger.InitLogger(logLevels[level])
	return nil
}

// ApplyStoredLogLevel is the boot-time counterpart: the panel setting wins over
// XUI_LOG_LEVEL once the database is readable, so the UI toggle survives a
// restart instead of silently reverting to the environment's value.
func ApplyStoredLogLevel() {
	level, err := (&SettingService{}).GetLogLevel()
	if err != nil {
		logger.Warning("log level setting unreadable, keeping the startup level:", err)
		return
	}
	if level == string(config.GetLogLevel()) {
		return
	}
	logger.InitLogger(logLevels[level])
	logger.Infof("log level set to %s by the panel settings", level)
}
