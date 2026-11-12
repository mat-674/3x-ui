package service

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func setupFrontSite(t *testing.T) *FrontSiteService {
	t.Helper()
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	return &FrontSiteService{}
}

// zipEntry is one member of a test archive; a non-zero mode carries the entry's
// unix mode so symlink and special files can be mirrored exactly as an attacker
// would pack them.
type zipEntry struct {
	name string
	body string
	mode fs.FileMode
}

func buildArchive(t *testing.T, entries ...zipEntry) *bytes.Reader {
	t.Helper()
	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", entry.name, err)
		}
		if _, err := member.Write([]byte(entry.body)); err != nil {
			t.Fatalf("write zip entry %q: %v", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}

func siteRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("XUI_DB_FOLDER"), "front", "sites")
}

func mustUpload(t *testing.T, svc *FrontSiteService, name string, entries ...zipEntry) *model.FrontSite {
	t.Helper()
	site, err := svc.Upload(name, buildArchive(t, entries...))
	if err != nil {
		t.Fatalf("upload %q: %v", name, err)
	}
	return site
}

// A ../ entry must never resolve outside the site directory: without the
// cleaning guard the extractor writes wherever the archive points.
func TestFrontSiteUploadRejectsZipSlip(t *testing.T) {
	svc := setupFrontSite(t)
	root := siteRoot(t)

	for _, name := range []string{"../escape.html", "../../escape.html", "a/../../escape.html", "a/../index.html"} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Upload("evil", buildArchive(t,
				zipEntry{name: name, body: "<html>pwned</html>"},
				zipEntry{name: "index.html", body: "<html>ok</html>"},
			))
			if !errors.Is(err, ErrFrontSiteZipSlip) {
				t.Fatalf("upload err = %v, want ErrFrontSiteZipSlip", err)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(root, "..", "escape.html")); err == nil {
		t.Fatal("a zip-slip entry escaped the site directory")
	}
	assertRootEmpty(t, root)
	if sites, err := svc.List(); err != nil || len(sites) != 0 {
		t.Fatalf("rejected uploads left rows: %v %v", sites, err)
	}
}

func TestFrontSiteUploadRejectsAbsoluteAndBackslashEntries(t *testing.T) {
	svc := setupFrontSite(t)

	for _, name := range []string{"/etc/passwd", "C:/windows/evil.html", `..\escape.html`, `dir\index.html`} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Upload("evil", buildArchive(t,
				zipEntry{name: name, body: "x"},
				zipEntry{name: "index.html", body: "<html>ok</html>"},
			))
			if !errors.Is(err, ErrFrontSiteZipSlip) {
				t.Fatalf("upload err = %v, want ErrFrontSiteZipSlip", err)
			}
		})
	}
	assertRootEmpty(t, siteRoot(t))
}

// A symlink or device entry would let the site read files outside its tree, so
// it is refused whether or not the target path looks harmless.
func TestFrontSiteUploadRejectsSymlinkEntry(t *testing.T) {
	cases := []struct {
		name string
		mode fs.FileMode
	}{
		{name: "symlink", mode: fs.ModeSymlink | 0o777},
		{name: "named pipe", mode: fs.ModeNamedPipe | 0o644},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := setupFrontSite(t)

			_, err := svc.Upload("evil", buildArchive(t,
				zipEntry{name: "index.html", body: "<html>ok</html>"},
				zipEntry{name: "leak.html", body: "/etc/passwd", mode: tc.mode},
			))
			if !errors.Is(err, ErrFrontSiteEntryKind) {
				t.Fatalf("upload err = %v, want ErrFrontSiteEntryKind", err)
			}
			assertRootEmpty(t, siteRoot(t))
			if sites, listErr := svc.List(); listErr != nil || len(sites) != 0 {
				t.Fatalf("rejected upload left rows: %v %v", sites, listErr)
			}
		})
	}
}

// The budget is spent on inflated bytes, so a huge entry aborts the whole upload
// instead of filling the disk, and the staging directory is gone afterwards.
func TestFrontSiteUploadRejectsOversizeEntry(t *testing.T) {
	svc := setupFrontSite(t)
	root := siteRoot(t)

	previous := frontSiteMaxBytes
	frontSiteMaxBytes = 1 << 10
	t.Cleanup(func() { frontSiteMaxBytes = previous })

	body := strings.Repeat("a", 4096)
	_, err := svc.Upload("big", buildArchive(t,
		zipEntry{name: "index.html", body: body},
	))
	if !errors.Is(err, ErrFrontSiteTooLarge) {
		t.Fatalf("upload err = %v, want ErrFrontSiteTooLarge", err)
	}
	assertRootEmpty(t, root)
}

// A zip whose declared sizes are small but which inflates past the budget is a
// decompression bomb; the stream guard catches it.
func TestFrontSiteUploadRejectsManySmallEntriesOverBudget(t *testing.T) {
	svc := setupFrontSite(t)

	previous := frontSiteMaxBytes
	frontSiteMaxBytes = 1 << 10
	t.Cleanup(func() { frontSiteMaxBytes = previous })

	entries := []zipEntry{{name: "index.html", body: "<html>ok</html>"}}
	for i := range 8 {
		entries = append(entries, zipEntry{
			name: "asset-" + strconv.Itoa(i) + ".txt",
			body: strings.Repeat("b", 200),
		})
	}
	_, err := svc.Upload("bomb", buildArchive(t, entries...))
	if !errors.Is(err, ErrFrontSiteTooLarge) {
		t.Fatalf("upload err = %v, want ErrFrontSiteTooLarge", err)
	}
	assertRootEmpty(t, siteRoot(t))
}

func TestFrontSiteUploadRejectsTooManyEntries(t *testing.T) {
	svc := setupFrontSite(t)

	previous := frontSiteMaxEntries
	frontSiteMaxEntries = 3
	t.Cleanup(func() { frontSiteMaxEntries = previous })

	_, err := svc.Upload("many", buildArchive(t,
		zipEntry{name: "index.html", body: "<html>ok</html>"},
		zipEntry{name: "a.txt", body: "a"},
		zipEntry{name: "b.txt", body: "b"},
		zipEntry{name: "c.txt", body: "c"},
	))
	if !errors.Is(err, ErrFrontSiteTooManyEntries) {
		t.Fatalf("upload err = %v, want ErrFrontSiteTooManyEntries", err)
	}
	assertRootEmpty(t, siteRoot(t))
}

// Depth is bounded so no upload can produce a tree the panel cannot walk.
func TestFrontSiteUploadRejectsDeepPath(t *testing.T) {
	svc := setupFrontSite(t)

	deep := "index.html"
	for range frontSitePathMax {
		deep = "d/" + deep
	}
	_, err := svc.Upload("deep", buildArchive(t,
		zipEntry{name: "index.html", body: "<html>ok</html>"},
		zipEntry{name: deep, body: "x"},
	))
	if !errors.Is(err, ErrFrontSitePathTooDeep) {
		t.Fatalf("upload err = %v, want ErrFrontSitePathTooDeep", err)
	}
	assertRootEmpty(t, siteRoot(t))
}

// The spool cap stops an oversized body before the archive is even opened.
func TestFrontSiteUploadRejectsOversizeArchive(t *testing.T) {
	svc := setupFrontSite(t)

	previous := MaxFrontSiteArchiveBytes
	MaxFrontSiteArchiveBytes = 64
	t.Cleanup(func() { MaxFrontSiteArchiveBytes = previous })

	_, err := svc.Upload("big", buildArchive(t,
		zipEntry{name: "index.html", body: strings.Repeat("a", 4096)},
	))
	if !errors.Is(err, ErrFrontSiteArchiveTooBig) {
		t.Fatalf("upload err = %v, want ErrFrontSiteArchiveTooBig", err)
	}
	assertRootEmpty(t, siteRoot(t))
}

// A zipped folder is the shape every archive tool emits, so the shared top level
// is stripped and the site root holds the files themselves.
func TestFrontSiteUploadStripsSingleTopLevelFolder(t *testing.T) {
	svc := setupFrontSite(t)

	site := mustUpload(t, svc, "corporate",
		zipEntry{name: "corp/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "corp/index.html", body: "<html>corp</html>"},
		zipEntry{name: "corp/css/site.css", body: "body{}"},
	)

	if site.EntryFile != "index.html" {
		t.Fatalf("entry file = %q, want index.html", site.EntryFile)
	}
	if _, err := os.Stat(filepath.Join(site.Dir, "index.html")); err != nil {
		t.Fatalf("index.html not at the site root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(site.Dir, "css", "site.css")); err != nil {
		t.Fatalf("css/site.css not kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(site.Dir, "corp")); err == nil {
		t.Fatal("the shared top-level folder was not stripped")
	}
	if site.Bytes != int64(len("<html>corp</html>")+len("body{}")) {
		t.Fatalf("bytes = %d, want the uncompressed total", site.Bytes)
	}
	if site.FileCount != 2 {
		t.Fatalf("file count = %d, want 2", site.FileCount)
	}
	if len(site.Sha256) != 64 {
		t.Fatalf("sha256 = %q, want a hex digest", site.Sha256)
	}
	if site.Active {
		t.Fatal("a fresh upload must not be active")
	}
	if rel, err := filepath.Rel(siteRoot(t), site.Dir); err != nil || rel == ".." {
		t.Fatalf("site dir %q is not under the front root: %v", site.Dir, err)
	}
}

// Several top-level entries mean the archive is already a site root; stripping
// anything here would drop files.
func TestFrontSiteUploadKeepsTreeWithoutSharedRoot(t *testing.T) {
	svc := setupFrontSite(t)

	site := mustUpload(t, svc, "portfolio",
		zipEntry{name: "index.html", body: "<html>home</html>"},
		zipEntry{name: "blog/post.html", body: "<html>post</html>"},
	)

	for _, rel := range []string{"index.html", filepath.Join("blog", "post.html")} {
		if _, err := os.Stat(filepath.Join(site.Dir, rel)); err != nil {
			t.Fatalf("%s missing after upload: %v", rel, err)
		}
	}
}

// The front needs one document to answer a decoy request with.
func TestFrontSiteUploadEntryFile(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipEntry
		want    string
		wantErr error
	}{
		{
			name:    "index at root",
			entries: []zipEntry{{name: "index.html", body: "a"}, {name: "about.html", body: "b"}},
			want:    "index.html",
		},
		{
			name:    "only html file",
			entries: []zipEntry{{name: "landing.html", body: "a"}, {name: "style.css", body: "b"}},
			want:    "landing.html",
		},
		{
			name:    "no html at all",
			entries: []zipEntry{{name: "readme.txt", body: "a"}},
			wantErr: ErrFrontSiteNoIndex,
		},
		{
			name:    "several html files, no index",
			entries: []zipEntry{{name: "a.html", body: "a"}, {name: "b.html", body: "b"}},
			wantErr: ErrFrontSiteNoIndex,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := setupFrontSite(t)
			site, err := svc.Upload("site", buildArchive(t, tc.entries...))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("upload err = %v, want %v", err, tc.wantErr)
				}
				assertRootEmpty(t, siteRoot(t))
				return
			}
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			if site.EntryFile != tc.want {
				t.Fatalf("entry file = %q, want %q", site.EntryFile, tc.want)
			}
		})
	}
}

// Activating is a transition, not a set: the previous site must lose the flag in
// the same transaction or two sites answer on the public port.
func TestFrontSiteActivateKeepsExactlyOneActive(t *testing.T) {
	svc := setupFrontSite(t)
	first := mustUpload(t, svc, "first", zipEntry{name: "index.html", body: "1"})
	second := mustUpload(t, svc, "second", zipEntry{name: "index.html", body: "2"})

	if err := svc.Activate(first.Id); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	if err := svc.Activate(second.Id); err != nil {
		t.Fatalf("activate second: %v", err)
	}

	sites, err := svc.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	active := []model.FrontSite{}
	for _, site := range sites {
		if site.Active {
			active = append(active, site)
		}
	}
	if len(active) != 1 || active[0].Id != second.Id {
		t.Fatalf("active sites = %+v, want exactly second (%d)", active, second.Id)
	}
}

func TestFrontSiteActivateUnknownId(t *testing.T) {
	svc := setupFrontSite(t)
	if err := svc.Activate(42); !errors.Is(err, ErrFrontSiteNotFound) {
		t.Fatalf("activate unknown = %v, want ErrFrontSiteNotFound", err)
	}
}

// A row whose directory vanished must not become the site the front answers
// with: activation would leave the public port serving an error.
func TestFrontSiteActivateRefusesMissingFiles(t *testing.T) {
	svc := setupFrontSite(t)
	site := mustUpload(t, svc, "gone", zipEntry{name: "index.html", body: "x"})
	if err := os.RemoveAll(site.Dir); err != nil {
		t.Fatalf("remove site dir: %v", err)
	}

	if err := svc.Activate(site.Id); err == nil {
		t.Fatal("activated a site with no files on disk")
	}
	stored, err := svc.Get(site.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Active {
		t.Fatal("the row was flagged active despite having no files")
	}
}

// Deleting the serving site would leave the front with nothing to answer with.
func TestFrontSiteDeleteRefusesActiveSite(t *testing.T) {
	svc := setupFrontSite(t)
	site := mustUpload(t, svc, "live", zipEntry{name: "index.html", body: "live"})
	if err := svc.Activate(site.Id); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if err := svc.Delete(site.Id); !errors.Is(err, ErrFrontSiteActive) {
		t.Fatalf("delete active = %v, want ErrFrontSiteActive", err)
	}
	if _, err := os.Stat(site.Dir); err != nil {
		t.Fatalf("active site files were removed: %v", err)
	}
	if _, err := svc.Dir(site.Id); err != nil {
		t.Fatalf("active site row was removed: %v", err)
	}
}

func TestFrontSiteDeleteRemovesRowAndDirectory(t *testing.T) {
	svc := setupFrontSite(t)
	site := mustUpload(t, svc, "old", zipEntry{name: "index.html", body: "old"})

	if err := svc.Delete(site.Id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(site.Dir); !os.IsNotExist(err) {
		t.Fatalf("site directory still present: %v", err)
	}
	if err := svc.Delete(site.Id); !errors.Is(err, ErrFrontSiteNotFound) {
		t.Fatalf("second delete = %v, want ErrFrontSiteNotFound", err)
	}
}

// Dir comes back from the database, so a tampered row must not turn into a
// filesystem handle anywhere on the host.
func TestFrontSiteDirRejectsTamperedPath(t *testing.T) {
	svc := setupFrontSite(t)
	site := mustUpload(t, svc, "guarded", zipEntry{name: "index.html", body: "x"})
	root := siteRoot(t)

	for _, dir := range []string{
		filepath.Join(root, "..", ".."),
		filepath.Dir(root),
		filepath.Join(root, "..", "elsewhere"),
	} {
		if err := database.GetDB().Model(&model.FrontSite{}).Where("id = ?", site.Id).Update("dir", dir).Error; err != nil {
			t.Fatalf("tamper dir: %v", err)
		}
		if _, err := svc.Dir(site.Id); !errors.Is(err, ErrFrontSiteDir) {
			t.Fatalf("Dir(%q) err = %v, want ErrFrontSiteDir", dir, err)
		}
		if err := svc.Activate(site.Id); !errors.Is(err, ErrFrontSiteDir) {
			t.Fatalf("Activate with dir %q err = %v, want ErrFrontSiteDir", dir, err)
		}
	}
}

func TestFrontSiteUploadRejectsDuplicateName(t *testing.T) {
	svc := setupFrontSite(t)
	mustUpload(t, svc, "corporate", zipEntry{name: "index.html", body: "a"})

	_, err := svc.Upload("corporate", buildArchive(t, zipEntry{name: "index.html", body: "b"}))
	if err == nil {
		t.Fatal("duplicate name accepted")
	}
	sites, listErr := svc.List()
	if listErr != nil || len(sites) != 1 {
		t.Fatalf("duplicate upload changed the table: %v %v", sites, listErr)
	}
}

func TestFrontSiteUploadRejectsEmptyName(t *testing.T) {
	svc := setupFrontSite(t)
	if _, err := svc.Upload("  ", buildArchive(t, zipEntry{name: "index.html", body: "a"})); err == nil {
		t.Fatal("empty name accepted")
	}
}

// assertRootEmpty proves a rejected upload left no staging or site directory.
func assertRootEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read front root: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("rejected upload left %v in %s", names, root)
	}
}
