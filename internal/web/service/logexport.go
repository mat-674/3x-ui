package service

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// Log sources the diagnostics page can download. The panel's own log is always
// written at DEBUG; the perimeter's logs are what explain a failed handshake,
// which is the usual reason someone opens this page.
const (
	LogSourceApp        = "app"
	LogSourceEdgeAccess = "edge-access"
	LogSourceEdgeError  = "edge-error"
)

const (
	// logExportDefaultLines keeps a default download readable in a browser tab.
	logExportDefaultLines = 5000
	// logExportMaxLines bounds the work one request can cause on a large file.
	logExportMaxLines = 50000
	// logExportMaxBytes bounds the memory a single export may take, so a request
	// for the tail of a huge log cannot be used to exhaust the panel.
	logExportMaxBytes = 32 << 20
)

// logFileName is the panel's own log file, written by the logger backend.
const logFileName = "3xui.log"

// LogSource is one downloadable file.
type LogSource struct {
	// Name is the API value, e.g. "app".
	Name string `json:"name" example:"app"`
	// Path is where the file lives, so an operator can also tail it by hand.
	Path string `json:"path" example:"/var/log/x-ui/3xui.log"`
	// Exists reports whether the panel has written it yet.
	Exists bool `json:"exists" example:"true"`
	// Size is the file size in bytes.
	Size int64 `json:"size" example:"204800"`
}

// AvailableLogs lists the files this panel can hand out, including whether each
// exists: a missing perimeter log usually means nginx never wrote one.
func (s *SettingService) AvailableLogs() []LogSource {
	candidates := []struct{ name, path string }{
		{LogSourceApp, filepath.Join(config.GetLogFolder(), logFileName)},
		{LogSourceEdgeAccess, filepath.Join(s.StateDir(), "logs", "access.log")},
		{LogSourceEdgeError, filepath.Join(s.StateDir(), "logs", "error.log")},
	}
	out := make([]LogSource, 0, len(candidates))
	for _, c := range candidates {
		source := LogSource{Name: c.name, Path: c.path}
		if info, err := os.Stat(c.path); err == nil && info.Mode().IsRegular() {
			source.Exists = true
			source.Size = info.Size()
		}
		out = append(out, source)
	}
	return out
}

// ExportLog returns the tail of one log source. Tailing rather than shipping the
// whole file keeps a rotated multi-megabyte log from filling the response, and is
// what an operator actually reads when something failed moments ago.
func (s *SettingService) ExportLog(source string, lines int) ([]byte, string, error) {
	source = normalizeLogSource(source)
	if lines <= 0 {
		lines = logExportDefaultLines
	}
	if lines > logExportMaxLines {
		lines = logExportMaxLines
	}

	var path string
	for _, candidate := range s.AvailableLogs() {
		if candidate.Name == source {
			path = candidate.Path
			break
		}
	}
	if path == "" {
		return nil, "", common.NewErrorf("unknown log source %q: use app, edge-access or edge-error", source)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, "", common.NewErrorf("the %s log does not exist yet (%s): start the component once and retry", source, path)
	}

	data, err := tailLines(path, lines)
	if err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("xui-%s-%s.log", source, time.Now().Format("20060102-150405"))
	return data, filename, nil
}

// normalizeLogSource accepts the API value, defaulting to the panel log.
func normalizeLogSource(source string) string {
	switch trimmed := trimLower(source); trimmed {
	case "", LogSourceApp:
		return LogSourceApp
	case LogSourceEdgeAccess, LogSourceEdgeError:
		return trimmed
	default:
		return trimmed
	}
}

// tailLines returns at most the last n lines of a file, reading backwards in
// chunks so the cost tracks the tail requested rather than the whole file.
func tailLines(path string, n int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return []byte{}, nil
	}

	const chunk = 64 << 10
	var (
		offset = size
		data   []byte
	)
	for offset > 0 {
		readSize := int64(chunk)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		block := make([]byte, readSize)
		if _, err := file.ReadAt(block, offset); err != nil {
			return nil, err
		}
		data = append(block, data...)
		// Enough newlines means the tail is complete; one extra proves the line
		// before the tail starts, so a partial first line is dropped below.
		if bytes.Count(data, newlineByte) > n || len(data) >= logExportMaxBytes {
			break
		}
	}

	lines := bytes.Split(data, newlineByte)
	// A file ending in a newline splits into a trailing empty element: counting it
	// as a line would return one line fewer than asked for.
	trailingNewline := len(lines) > 0 && len(lines[len(lines)-1]) == 0
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := bytes.Join(lines, newlineByte)
	if trailingNewline {
		out = append(out, newlineByte...)
	}
	return out, nil
}

var newlineByte = []byte("\n")

// trimLower is a tiny helper kept local so the export path does no allocation
// beyond the value it is given.
func trimLower(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	out := []byte(s[start:end])
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
