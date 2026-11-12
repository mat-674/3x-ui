package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
)

// A switch that does not persist is worse than none: the panel would silently
// drop back to the old level on the next settings save.
func TestSetLogLevelPersistsAndNormalizes(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SettingService{}

	level, err := svc.SetLogLevel("  DEBUG ")
	if err != nil {
		t.Fatalf("SetLogLevel: %v", err)
	}
	if level != LogLevelDebug {
		t.Fatalf("level = %q, want the normalised debug", level)
	}
	stored, err := svc.GetLogLevel()
	if err != nil {
		t.Fatalf("GetLogLevel: %v", err)
	}
	if stored != LogLevelDebug {
		t.Fatalf("stored level = %q, want debug to survive the round trip", stored)
	}
}

// A typo must be rejected loudly: silently falling back would leave the operator
// believing debug logging is on.
func TestSetLogLevelRejectsUnknownLevels(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SettingService{}
	for _, bad := range []string{"verbose", "trace", "debug2"} {
		if _, err := svc.SetLogLevel(bad); err == nil {
			t.Fatalf("SetLogLevel(%q) was accepted", bad)
		}
	}
	if got, _ := svc.GetLogLevel(); got != LogLevelInfo {
		t.Fatalf("level = %q after rejected writes, want the default info", got)
	}
}

// An empty stored value must resolve to info: it is what a pre-upgrade database
// has, and it must not block every other setting from saving.
func TestGetLogLevelTreatsEmptyAsInfo(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SettingService{}
	if err := svc.setString("logLevel", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := svc.GetLogLevel()
	if err != nil {
		t.Fatalf("GetLogLevel: %v", err)
	}
	if got != LogLevelInfo {
		t.Fatalf("level = %q, want info for an empty value", got)
	}
}

// tailLines is the whole point of the export: a rotated log must not be shipped
// whole, and the last line must arrive intact.
func TestTailLinesReturnsTheRequestedTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	var body strings.Builder
	for i := 1; i <= 100; i++ {
		body.WriteString("line-")
		body.WriteString(strings.Repeat("x", i))
		body.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := tailLines(path, 5)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want exactly 5:\n%s", len(lines), data)
	}
	if !strings.HasPrefix(lines[0], "line-") {
		t.Fatalf("the tail starts mid-line: %q", lines[0])
	}
	if lines[len(lines)-1] != "line-"+strings.Repeat("x", 100) {
		t.Fatalf("the last line is not the file's last line: %q", lines[len(lines)-1])
	}
}

// Asking for more lines than the file holds must return the file, not an error
// and not padded content.
func TestTailLinesHandlesFilesSmallerThanTheRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.log")
	if err := os.WriteFile(path, []byte("only\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := tailLines(path, 500)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if string(data) != "only\ntwo\n" {
		t.Fatalf("tail = %q, want the whole file", data)
	}
}

func TestTailLinesOnEmptyFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := tailLines(path, 10)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("tail of an empty file = %q, want empty", data)
	}
}

// The export must refuse an unknown source rather than guessing a path: an
// attacker-chosen value must never become a file read.
func TestExportLogRejectsUnknownSource(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SettingService{}

	for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "xray", "app/../../etc/passwd"} {
		if _, _, err := svc.ExportLog(bad, 10); err == nil {
			t.Fatalf("ExportLog(%q) was accepted, so the source is a path traversal", bad)
		}
	}
}

// The sources list must be honest about what exists: a perimeter that never
// started writes no logs, and the UI disables exactly those.
func TestAvailableLogsReportsExistence(t *testing.T) {
	dbFolder := t.TempDir()
	logFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_LOG_FOLDER", logFolder)
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SettingService{}

	sources := svc.AvailableLogs()
	if len(sources) != 3 {
		t.Fatalf("got %d sources, want app + edge-access + edge-error", len(sources))
	}
	byName := make(map[string]LogSource, len(sources))
	for _, source := range sources {
		byName[source.Name] = source
	}
	if byName[LogSourceApp].Exists {
		t.Fatal("the panel log reported as existing before the backend wrote one")
	}

	// Write the app log; the perimeter's must stay absent until nginx runs.
	appPath := byName[LogSourceApp].Path
	if err := os.MkdirAll(filepath.Dir(appPath), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(appPath, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write app log: %v", err)
	}
	// The perimeter log path is derived from the settings state dir.
	edgeLog := filepath.Join(svc.StateDir(), "logs", "access.log")
	if err := os.MkdirAll(filepath.Dir(edgeLog), 0o750); err != nil {
		t.Fatalf("mkdir edge logs: %v", err)
	}
	if err := os.WriteFile(edgeLog, []byte("GET /\n"), 0o600); err != nil {
		t.Fatalf("write edge log: %v", err)
	}

	refreshed := make(map[string]LogSource)
	for _, source := range svc.AvailableLogs() {
		refreshed[source.Name] = source
	}
	if !refreshed[LogSourceApp].Exists || refreshed[LogSourceApp].Size != int64(len("hello\n")) {
		t.Fatalf("app source = %+v, want it present with its real size", refreshed[LogSourceApp])
	}
	if !refreshed[LogSourceEdgeAccess].Exists {
		t.Fatal("the perimeter access log was written but reported as missing")
	}
	if refreshed[LogSourceEdgeError].Exists {
		t.Fatal("the perimeter error log reported as existing although nothing wrote it")
	}
}

// Downloading a source that does not exist must explain itself, not hand back an
// empty file the operator would mistake for "no errors".
func TestExportLogExplainsAMissingFile(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &SettingService{}

	_, _, err := svc.ExportLog(LogSourceEdgeError, 10)
	if err == nil {
		t.Fatal("exporting a missing log succeeded, so the operator gets an empty file and assumes nothing went wrong")
	}
	if !strings.Contains(err.Error(), LogSourceEdgeError) {
		t.Fatalf("error %q does not name the missing source", err)
	}
}
