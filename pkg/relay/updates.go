package relay

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// errUpdateUnavailable means there is no usable published build for an app.
var errUpdateUnavailable = errors.New("update unavailable")

// updateManifest is the local file next to the APK (contracts.md §5). Its API
// body, model.UpdateInfo, is the same metadata without apk.
type updateManifest struct {
	model.UpdateInfo
	APK string `json:"apk"`
}

// updateDir returns the directory published Android builds live in. The
// config sets it from AW_UPDATE_DIR (default <AW_DATA_DIR>/updates).
func (s *Server) updateDir() string {
	if s.cfg == nil {
		return ""
	}
	if s.cfg.UpdateDir != "" {
		return s.cfg.UpdateDir
	}
	if s.cfg.DataDir != "" {
		return filepath.Join(s.cfg.DataDir, "updates")
	}
	return ""
}

// readUpdate reads and validates app's metadata and returns the APK path. It
// checks the file's size now, so a half-finished publish is not advertised.
func (s *Server) readUpdate(app string) (model.UpdateInfo, string, error) {
	dir := s.updateDir()
	if dir == "" {
		return model.UpdateInfo{}, "", errUpdateUnavailable
	}

	data, err := os.ReadFile(filepath.Join(dir, app+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return model.UpdateInfo{}, "", errUpdateUnavailable
	}
	if err != nil {
		return model.UpdateInfo{}, "", fmt.Errorf("read %s update metadata: %w", app, err)
	}

	var manifest updateManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return model.UpdateInfo{}, "", fmt.Errorf("decode %s update metadata: %w", app, err)
	}
	if err := validateUpdate(app, manifest); err != nil {
		return model.UpdateInfo{}, "", err
	}

	path := filepath.Join(dir, manifest.APK)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return model.UpdateInfo{}, "", errUpdateUnavailable
	}
	if err != nil {
		return model.UpdateInfo{}, "", fmt.Errorf("open %s update APK: %w", app, err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return model.UpdateInfo{}, "", fmt.Errorf("stat %s update APK: %w", app, err)
	}
	if !st.Mode().IsRegular() {
		return model.UpdateInfo{}, "", fmt.Errorf("%s update APK is not a regular file", app)
	}
	if st.Size() != manifest.Size {
		return model.UpdateInfo{}, "", fmt.Errorf("%s update APK is %d bytes, metadata says %d", app, st.Size(), manifest.Size)
	}

	return manifest.UpdateInfo, path, nil
}

func validateUpdate(app string, manifest updateManifest) error {
	if manifest.App != app {
		return fmt.Errorf("%s update metadata names app %q", app, manifest.App)
	}
	if manifest.VersionCode <= 0 {
		return fmt.Errorf("%s update metadata has version_code %d", app, manifest.VersionCode)
	}
	if strings.TrimSpace(manifest.VersionName) == "" {
		return fmt.Errorf("%s update metadata has an empty version_name", app)
	}
	if len(manifest.SHA256) != 64 {
		return fmt.Errorf("%s update metadata has a SHA-256 of %d characters", app, len(manifest.SHA256))
	}
	if _, err := hex.DecodeString(manifest.SHA256); err != nil {
		return fmt.Errorf("%s update metadata SHA-256: %w", app, err)
	}
	if manifest.Size <= 0 {
		return fmt.Errorf("%s update metadata has size %d", app, manifest.Size)
	}
	// The publisher writes <app>-<version_code>.apk in this directory. A bare
	// name with that prefix is the only thing the relay serves: a manifest
	// must never point at store.json or outside the directory.
	if manifest.APK == "" ||
		filepath.Base(manifest.APK) != manifest.APK ||
		strings.ContainsAny(manifest.APK, `/\`) ||
		!strings.HasPrefix(manifest.APK, app+"-") ||
		!strings.HasSuffix(manifest.APK, ".apk") {
		return fmt.Errorf("%s update metadata has an invalid apk name %q", app, manifest.APK)
	}
	return nil
}

func (s *Server) writeUpdateError(w http.ResponseWriter, app string, err error) {
	if errors.Is(err, errUpdateUnavailable) {
		writeError(w, model.ErrUpdateUnavailable, "no update published for this app")
		return
	}
	slog.Error("serving android update", "app", app, "error", err)
	writeError(w, model.ErrInternal, "the published update is unusable")
}

// GET /v1/updates/{app}
func (s *Server) updateInfo(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if !model.ValidUpdateApp(app) {
		writeError(w, model.ErrInvalidRequest, `app must be "phone" or "watch"`)
		return
	}
	info, _, err := s.readUpdate(app)
	if err != nil {
		s.writeUpdateError(w, app, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// GET /v1/updates/{app}/apk
func (s *Server) updateAPK(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if !model.ValidUpdateApp(app) {
		writeError(w, model.ErrInvalidRequest, `app must be "phone" or "watch"`)
		return
	}
	info, path, err := s.readUpdate(app)
	if err != nil {
		s.writeUpdateError(w, app, err)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		s.writeUpdateError(w, app, fmt.Errorf("open %s update APK: %w", app, err))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != info.Size {
		s.writeUpdateError(w, app, fmt.Errorf("%s update APK changed while serving it", app))
		return
	}

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="agent-watch-%s-%d.apk"`, info.App, info.VersionCode))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}
