package service

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Sentinel failures of the front-site store. Each names one guard, so a caller
// (and a test) can tell a rejected archive from a broken one.
var (
	ErrFrontSiteZipSlip        = errors.New("front site archive entry escapes the site directory")
	ErrFrontSiteEntryKind      = errors.New("front site archive entry is not a regular file")
	ErrFrontSiteDuplicateEntry = errors.New("front site archive has two entries for one path")
	ErrFrontSiteTooLarge       = errors.New("front site exceeds the uncompressed size limit")
	ErrFrontSiteArchiveTooBig  = errors.New("front site archive exceeds the upload limit")
	ErrFrontSiteTooManyEntries = errors.New("front site archive has too many entries")
	ErrFrontSitePathTooDeep    = errors.New("front site archive entry nests too deep")
	ErrFrontSiteNoIndex        = errors.New("no index.html")
	ErrFrontSiteActive         = errors.New("the active front site cannot be deleted")
	ErrFrontSiteNotFound       = errors.New("front site not found")
	ErrFrontSiteDir            = errors.New("front site directory is outside the front root")
)

const (
	frontSiteNameMax = 128
	frontSitePathMax = 16
)

// MaxFrontSiteArchiveBytes caps the uploaded .zip; the extracted site is capped
// separately, so a small archive cannot inflate past its own budget.
//
// Vars, not consts, because the arity of an archive is attacker input and a test
// needs to reach each limit without writing a gigabyte to disk.
var (
	MaxFrontSiteArchiveBytes int64 = 128 << 20
	frontSiteMaxBytes        int64 = 128 << 20
	frontSiteMaxEntries            = 5000
)

// FrontSiteService stores the decoy websites the TLS front answers with. Each
// upload is one directory under the panel's front root; only metadata is in the
// database, so a panel export never carries a whole website.
type FrontSiteService struct{}

// Upload extracts a .zip into a fresh directory and records it. Every entry is
// validated before it is written, so a rejected archive leaves no trace.
func (s *FrontSiteService) Upload(name string, archive io.Reader) (*model.FrontSite, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return nil, errors.New("front site name is required")
	case len(name) > frontSiteNameMax:
		return nil, fmt.Errorf("front site name too long (max %d)", frontSiteNameMax)
	}
	root, err := frontSiteRoot()
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(root, ".upload-")
	if err != nil {
		return nil, fmt.Errorf("create front site staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	sum, err := spoolFrontArchive(filepath.Join(stage, "archive.zip"), archive)
	if err != nil {
		return nil, err
	}
	staged := filepath.Join(stage, "site")
	if err := os.Mkdir(staged, 0o700); err != nil {
		return nil, fmt.Errorf("create front site staging directory: %w", err)
	}
	reader, err := zip.OpenReader(filepath.Join(stage, "archive.zip"))
	if err != nil {
		return nil, fmt.Errorf("open front site archive: %w", err)
	}
	defer func() { _ = reader.Close() }()

	stat, err := extractFrontSite(reader.File, staged)
	if err != nil {
		return nil, err
	}
	site := &model.FrontSite{
		Name:      name,
		EntryFile: stat.entry,
		Bytes:     stat.bytes,
		FileCount: stat.files,
		Sha256:    sum,
	}
	// The directory name is the row id, so the row is inserted before the move
	// and rolled back with it: a failed upload leaves neither.
	moved := ""
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		var taken int64
		if err := tx.Model(&model.FrontSite{}).Where("name = ?", name).Count(&taken).Error; err != nil {
			return err
		}
		if taken > 0 {
			return fmt.Errorf("front site %q already exists", name)
		}
		if err := tx.Create(site).Error; err != nil {
			return err
		}
		dir := filepath.Join(root, strconv.Itoa(site.Id))
		if err := os.Rename(staged, dir); err != nil {
			return fmt.Errorf("move front site into place: %w", err)
		}
		moved = dir
		site.Dir = dir
		return tx.Model(&model.FrontSite{}).Where("id = ?", site.Id).Update("dir", dir).Error
	})
	if err != nil {
		if moved != "" {
			_ = os.RemoveAll(moved)
		}
		return nil, err
	}
	return site, nil
}

// List returns every stored site, oldest first.
func (s *FrontSiteService) List() ([]model.FrontSite, error) {
	sites := []model.FrontSite{}
	if err := database.GetDB().Order("id asc").Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

// Get returns one site row by id.
func (s *FrontSiteService) Get(id int) (*model.FrontSite, error) {
	site := model.FrontSite{}
	if err := database.GetDB().First(&site, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: id %d", ErrFrontSiteNotFound, id)
		}
		return nil, err
	}
	return &site, nil
}

// Activate makes id the only active site, so the front has one document to serve.
func (s *FrontSiteService) Activate(id int) error {
	site, err := s.Get(id)
	if err != nil {
		return err
	}
	dir, err := frontSiteStoredDir(site)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("front site %q has no files on disk", site.Name)
	}
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.FrontSite{}).Where("active = ?", true).Update("active", false).Error; err != nil {
			return err
		}
		return tx.Model(&model.FrontSite{}).Where("id = ?", id).Update("active", true).Error
	})
}

// Delete removes a site's files and its row. The active site is refused: it is
// the one the front is answering with.
func (s *FrontSiteService) Delete(id int) error {
	site, err := s.Get(id)
	if err != nil {
		return err
	}
	dir, err := frontSiteStoredDir(site)
	if err != nil {
		return err
	}
	// Deleting conditionally on active=false is the guard itself, so an Activate
	// racing this call cannot drop the site out from under the front.
	res := database.GetDB().Where("id = ? AND active = ?", id, false).Delete(&model.FrontSite{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: %q", ErrFrontSiteActive, site.Name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove front site files: %w", err)
	}
	return nil
}

// Dir returns the site's directory, re-validated against the front root because
// the path comes back from the database.
func (s *FrontSiteService) Dir(id int) (string, error) {
	site, err := s.Get(id)
	if err != nil {
		return "", err
	}
	return frontSiteStoredDir(site)
}

// frontSiteRoot is the panel folder's front/sites, created 0700 on demand.
func frontSiteRoot() (string, error) {
	root := filepath.Join(config.GetDBFolderPath(), "front", "sites")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create front site root: %w", err)
	}
	return root, nil
}

func frontSiteStoredDir(site *model.FrontSite) (string, error) {
	root, err := frontSiteRoot()
	if err != nil {
		return "", err
	}
	dir, err := filepath.Abs(site.Dir)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrFrontSiteDir, site.Dir)
	}
	if !frontSiteInside(root, dir) {
		return "", fmt.Errorf("%w: %s", ErrFrontSiteDir, site.Dir)
	}
	return dir, nil
}

// frontSiteInside reports whether target is strictly below base.
func frontSiteInside(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func spoolFrontArchive(dst string, archive io.Reader) (string, error) {
	file, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("spool front site archive: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(archive, MaxFrontSiteArchiveBytes+1))
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		return "", fmt.Errorf("read front site archive: %w", copyErr)
	case closeErr != nil:
		return "", fmt.Errorf("spool front site archive: %w", closeErr)
	case written > MaxFrontSiteArchiveBytes:
		return "", fmt.Errorf("%w (max %d bytes)", ErrFrontSiteArchiveTooBig, MaxFrontSiteArchiveBytes)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type frontSiteStat struct {
	entry string
	bytes int64
	files int
}

type frontSiteEntry struct {
	file *zip.File
	rel  string
	dir  bool
}

func extractFrontSite(members []*zip.File, dest string) (frontSiteStat, error) {
	entries, err := frontSiteEntries(members)
	if err != nil {
		return frontSiteStat{}, err
	}
	remaining := frontSiteMaxBytes
	stat := frontSiteStat{}
	for _, entry := range entries {
		target := filepath.Join(dest, filepath.FromSlash(entry.rel))
		if !frontSiteInside(dest, target) {
			return frontSiteStat{}, fmt.Errorf("%w: %s", ErrFrontSiteZipSlip, entry.file.Name)
		}
		if entry.dir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return frontSiteStat{}, fmt.Errorf("create front site directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return frontSiteStat{}, fmt.Errorf("create front site directory: %w", err)
		}
		written, err := extractFrontSiteEntry(entry.file, target, remaining)
		if err != nil {
			return frontSiteStat{}, err
		}
		remaining -= written
		stat.bytes += written
		stat.files++
	}
	stat.entry, err = frontSiteEntryFile(dest)
	return stat, err
}

func extractFrontSiteEntry(entry *zip.File, target string, remaining int64) (int64, error) {
	src, err := entry.Open()
	if err != nil {
		return 0, fmt.Errorf("read front site entry %s: %w", entry.Name, err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create front site file %s: %w", entry.Name, err)
	}
	// UncompressedSize64 is the archive's claim, not a measurement: the budget is
	// spent on bytes actually inflated.
	written, copyErr := io.Copy(dst, io.LimitReader(src, remaining+1))
	closeErr := dst.Close()
	switch {
	case copyErr != nil:
		return written, fmt.Errorf("extract front site entry %s: %w", entry.Name, copyErr)
	case closeErr != nil:
		return written, fmt.Errorf("extract front site entry %s: %w", entry.Name, closeErr)
	case written > remaining:
		return written, fmt.Errorf("%w (max %d bytes)", ErrFrontSiteTooLarge, frontSiteMaxBytes)
	}
	return written, nil
}

// frontSiteEntries validates every member before anything is written: an archive
// that fails here costs no disk and no cleanup.
func frontSiteEntries(members []*zip.File) ([]frontSiteEntry, error) {
	if len(members) > frontSiteMaxEntries {
		return nil, fmt.Errorf("%w (max %d)", ErrFrontSiteTooManyEntries, frontSiteMaxEntries)
	}
	entries := make([]frontSiteEntry, 0, len(members))
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		rel, dir, err := frontSiteEntryName(member.Name)
		if err != nil {
			return nil, err
		}
		if rel == "" {
			continue
		}
		mode := member.Mode()
		dir = dir || mode.IsDir()
		// A symlink, device or FIFO is not a regular file: extracted, it would
		// either reach outside the site tree or hang a future static read.
		if !dir && !mode.IsRegular() {
			return nil, fmt.Errorf("%w: %s", ErrFrontSiteEntryKind, member.Name)
		}
		if seen[rel] {
			return nil, fmt.Errorf("%w: %s", ErrFrontSiteDuplicateEntry, rel)
		}
		if depth := strings.Count(rel, "/") + 1; depth > frontSitePathMax {
			return nil, fmt.Errorf("%w: %s nests %d deep", ErrFrontSitePathTooDeep, rel, depth)
		}
		seen[rel] = true
		entries = append(entries, frontSiteEntry{file: member, rel: rel, dir: dir})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: the archive is empty", ErrFrontSiteNoIndex)
	}
	return stripFrontSiteCommonRoot(entries), nil
}

// frontSiteEntryName turns a zip member name into a relative slash path and one
// error per rejection reason: absolute, drive-letter, backslash, or any "..".
func frontSiteEntryName(name string) (rel string, dir bool, err error) {
	reject := func() (string, bool, error) {
		return "", false, fmt.Errorf("%w: %q", ErrFrontSiteZipSlip, name)
	}
	if name == "" || strings.ContainsRune(name, '\\') || path.IsAbs(name) {
		return reject()
	}
	// A drive letter is absolute on Windows while path.IsAbs only knows slash
	// roots, so "C:/x" would otherwise be extracted relative to the panel.
	if len(name) >= 2 && name[1] == ':' {
		return reject()
	}
	for _, element := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if element == ".." {
			return reject()
		}
	}
	dir = strings.HasSuffix(name, "/")
	rel = path.Clean(name)
	if rel == "." {
		return "", dir, nil
	}
	return rel, dir, nil
}

// stripFrontSiteCommonRoot drops the one folder a zipped directory adds, so a
// site is served from the archive's own root either way.
func stripFrontSiteCommonRoot(entries []frontSiteEntry) []frontSiteEntry {
	root := ""
	nested := false
	for _, entry := range entries {
		first, rest, _ := strings.Cut(entry.rel, "/")
		if first != root {
			if root != "" {
				return entries
			}
			root = first
		}
		if rest != "" {
			nested = true
		}
	}
	if root == "" || !nested {
		return entries
	}
	stripped := make([]frontSiteEntry, 0, len(entries))
	for _, entry := range entries {
		rel, ok := strings.CutPrefix(entry.rel, root+"/")
		if !ok {
			continue
		}
		entry.rel = rel
		stripped = append(stripped, entry)
	}
	return stripped
}

// frontSiteEntryFile picks the document a decoy request answers with.
func frontSiteEntryFile(dest string) (string, error) {
	documents := make([]string, 0, 4)
	err := filepath.WalkDir(dest, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dest, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(path.Ext(rel), ".html") {
			documents = append(documents, rel)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan front site files: %w", err)
	}
	for _, rel := range documents {
		if strings.EqualFold(rel, "index.html") {
			return rel, nil
		}
	}
	if len(documents) == 1 {
		return documents[0], nil
	}
	if len(documents) == 0 {
		return "", ErrFrontSiteNoIndex
	}
	return "", fmt.Errorf("%w: %d .html files and none named index.html", ErrFrontSiteNoIndex, len(documents))
}
