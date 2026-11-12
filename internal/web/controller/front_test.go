package controller

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	xuilogger "github.com/mhsanaei/3x-ui/v3/internal/logger"
)

func newFrontTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	xuilogger.InitLogger(logging.ERROR)
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))

	engine := gin.New()
	NewFrontController(engine.Group("/panel/api"))
	return engine
}

type frontEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func doFrontReq(t *testing.T, engine *gin.Engine, method, path string, body *bytes.Buffer, contentType string) frontEnvelope {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: status %d, body=%s", method, path, w.Code, w.Body.String())
	}
	var env frontEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: decode envelope: %v body=%s", method, path, err, w.Body.String())
	}
	return env
}

// uploadFrontSite posts a one-page site as multipart, exactly as the API docs
// describe it: field "file" plus field "name".
func uploadFrontSite(t *testing.T, engine *gin.Engine, name string) *model.FrontSite {
	t.Helper()
	archive := &bytes.Buffer{}
	writer := zip.NewWriter(archive)
	member, err := writer.Create("index.html")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := member.Write([]byte("<html>" + name + "</html>")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	part, err := form.CreateFormFile("file", name+".zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := form.WriteField("name", name); err != nil {
		t.Fatalf("write name field: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	env := doFrontReq(t, engine, http.MethodPost, "/panel/api/front/sites/upload", body, form.FormDataContentType())
	if !env.Success {
		t.Fatalf("upload %q failed: %s", name, env.Msg)
	}
	var site model.FrontSite
	if err := json.Unmarshal(env.Obj, &site); err != nil {
		t.Fatalf("decode uploaded site: %v", err)
	}
	return &site
}

// The upload contract is the two multipart field names; a rename here silently
// breaks every documented client.
func TestFrontControllerUploadListActivateDelete(t *testing.T) {
	engine := newFrontTestEngine(t)

	first := uploadFrontSite(t, engine, "first")
	if first.Id == 0 || first.Name != "first" || first.EntryFile != "index.html" {
		t.Fatalf("uploaded site = %+v", first)
	}

	list := doFrontReq(t, engine, http.MethodGet, "/panel/api/front/sites", &bytes.Buffer{}, "")
	if !list.Success {
		t.Fatalf("list failed: %s", list.Msg)
	}
	var sites []model.FrontSite
	if err := json.Unmarshal(list.Obj, &sites); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(sites) != 1 || sites[0].Id != first.Id {
		t.Fatalf("list = %+v, want the uploaded site", sites)
	}

	second := uploadFrontSite(t, engine, "second")
	activate := doFrontReq(t, engine, http.MethodPost, "/panel/api/front/sites/"+strconv.Itoa(second.Id)+"/activate", &bytes.Buffer{}, "")
	if !activate.Success {
		t.Fatalf("activate failed: %s", activate.Msg)
	}
	var activated model.FrontSite
	if err := json.Unmarshal(activate.Obj, &activated); err != nil {
		t.Fatalf("decode activated site: %v", err)
	}
	if !activated.Active || activated.Id != second.Id {
		t.Fatalf("activated site = %+v", activated)
	}

	refused := doFrontReq(t, engine, http.MethodPost, "/panel/api/front/sites/"+strconv.Itoa(second.Id)+"/delete", &bytes.Buffer{}, "")
	if refused.Success {
		t.Fatal("deleting the active site reported success")
	}

	deleted := doFrontReq(t, engine, http.MethodPost, "/panel/api/front/sites/"+strconv.Itoa(first.Id)+"/delete", &bytes.Buffer{}, "")
	if !deleted.Success {
		t.Fatalf("deleting an inactive site failed: %s", deleted.Msg)
	}
}
