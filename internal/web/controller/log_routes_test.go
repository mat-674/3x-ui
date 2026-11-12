package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
)

// newLogRoutesTestEngine wires the log routes with no session middleware, so the
// handler behaviour under test is not masked by an auth rejection.
func newLogRoutesTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	a := &ServerController{}
	group := engine.Group("/panel/api/server")
	group.GET("/logs/sources", a.getLogSources)
	group.GET("/logs/export", a.exportLog)
	group.POST("/logLevel", a.setLogLevel)
	return engine
}

// The download must be an attachment the browser saves, not JSON the panel
// renders: an operator forwarding this to someone needs a file.
func TestExportLogReturnsAnAttachment(t *testing.T) {
	logFolder := t.TempDir()
	t.Setenv("XUI_LOG_FOLDER", logFolder)
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	body := "line one\nline two\nline three\n"
	if err := os.WriteFile(filepath.Join(logFolder, "3xui.log"), []byte(body), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	rec := httptest.NewRecorder()
	newLogRoutesTestEngine(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/api/server/logs/export?source=app&lines=2", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("Content-Disposition = %q, want an attachment", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	// lines=2 must actually limit the body: the operator asked for the tail.
	if got := rec.Body.String(); strings.Contains(got, "line one") || !strings.Contains(got, "line three") {
		t.Fatalf("body = %q, want only the last two lines", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store so a log is never cached", got)
	}
}

// Requesting a log that does not exist must answer with the panel's error
// envelope: an empty file would read as "nothing went wrong".
func TestExportLogMissingFileIsAnError(t *testing.T) {
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	rec := httptest.NewRecorder()
	newLogRoutesTestEngine(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/api/server/logs/export?source=edge-error", nil))

	var envelope struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body is not the panel's JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if envelope.Success {
		t.Fatalf("a missing log answered success=true; body=%s", rec.Body.String())
	}
	if !strings.Contains(envelope.Msg, "edge-error") {
		t.Fatalf("msg %q does not name the missing source", envelope.Msg)
	}
}

// The sources list must be JSON the UI can render, including the sizes it uses
// to decide what to offer.
func TestGetLogSourcesAnswersTheList(t *testing.T) {
	logFolder := t.TempDir()
	t.Setenv("XUI_LOG_FOLDER", logFolder)
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	if err := os.WriteFile(filepath.Join(logFolder, "3xui.log"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	rec := httptest.NewRecorder()
	newLogRoutesTestEngine(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/api/server/logs/sources", nil))

	var envelope struct {
		Success bool `json:"success"`
		Obj     []struct {
			Name   string `json:"name"`
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
			Size   int64  `json:"size"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if !envelope.Success || len(envelope.Obj) != 3 {
		t.Fatalf("envelope = %+v, want success with three sources", envelope)
	}
	app := envelope.Obj[0]
	if app.Name != "app" || !app.Exists || app.Size != int64(len("hello\n")) {
		t.Fatalf("app source = %+v, want it present with its size", app)
	}
}

// The level endpoint is what the UI toggle calls; it must reject a typo rather
// than quietly doing nothing.
func TestSetLogLevelRejectsAnUnknownLevel(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/logLevel", strings.NewReader(`{"level":"verbose"}`))
	req.Header.Set("Content-Type", "application/json")
	newLogRoutesTestEngine(t).ServeHTTP(rec, req)

	var envelope struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body is not the panel's JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if envelope.Success {
		t.Fatalf("an unknown level answered success=true; body=%s", rec.Body.String())
	}
	if !strings.Contains(envelope.Msg, "verbose") {
		t.Fatalf("msg %q does not quote the rejected level", envelope.Msg)
	}
}

func TestSetLogLevelAppliesTheLevel(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/logLevel", strings.NewReader(`{"level":"debug"}`))
	req.Header.Set("Content-Type", "application/json")
	newLogRoutesTestEngine(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"level":"debug"`) {
		t.Fatalf("body = %s, want the applied level echoed back", rec.Body.String())
	}
}
