package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// updateTestServer returns a relay with a device token and an empty update
// directory. The caller publishes builds into dir.
func updateTestServer(t *testing.T) (server *Server, ts *httptest.Server, dir, token string) {
	t.Helper()
	dir = t.TempDir()
	server, ts = setupTestServerWith(t, func(cfg *Config) {
		cfg.UpdateDir = dir
	})
	token, err := GenerateDeviceToken()
	if err != nil {
		t.Fatalf("GenerateDeviceToken: %v", err)
	}
	if _, err := server.Store().AddDevice("Phone", Sha256Hex(token)); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	return server, ts, dir, token
}

// publishUpdate writes the metadata and APK the way make publish-android does:
// an immutable <app>-<code>.apk and a <app>.json that names it.
func publishUpdate(t *testing.T, dir, app string, code int, apk []byte) model.UpdateInfo {
	t.Helper()
	name := fmt.Sprintf("%s-%d.apk", app, code)
	if err := os.WriteFile(filepath.Join(dir, name), apk, 0o600); err != nil {
		t.Fatalf("write APK: %v", err)
	}
	sum := sha256.Sum256(apk)
	info := model.UpdateInfo{
		App:         app,
		VersionCode: code,
		VersionName: fmt.Sprintf("1.4.%d", code),
		SHA256:      hex.EncodeToString(sum[:]),
		Size:        int64(len(apk)),
	}
	writeManifest(t, dir, updateManifest{UpdateInfo: info, APK: name})
	return info
}

func writeManifest(t *testing.T, dir string, manifest updateManifest) {
	t.Helper()
	writeManifestAs(t, dir, manifest.App, manifest)
}

func writeManifestAs(t *testing.T, dir, app string, manifest updateManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, app+".json"), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func getUpdate(t *testing.T, ts *httptest.Server, token, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func decodeError(t *testing.T, resp *http.Response) model.ErrorResponse {
	t.Helper()
	defer resp.Body.Close()
	var body model.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return body
}

func TestUpdates_InfoAndAPK(t *testing.T) {
	for _, app := range []string{model.UpdateAppPhone, model.UpdateAppWatch} {
		t.Run(app, func(t *testing.T) {
			_, ts, dir, token := updateTestServer(t)

			publishUpdate(t, dir, app, 5, []byte("apk bytes"))

			resp := getUpdate(t, ts, token, "/v1/updates/"+app)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("metadata status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Fatalf("metadata Content-Type = %q", got)
			}
			var info model.UpdateInfo
			if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
				t.Fatalf("decode metadata: %v", err)
			}
			resp.Body.Close()

			if info.App != app || info.VersionCode != 5 || info.Size != int64(len("apk bytes")) {
				t.Fatalf("metadata = %+v", info)
			}
			if len(info.SHA256) != 64 {
				t.Fatalf("metadata SHA-256 = %q", info.SHA256)
			}

			resp = getUpdate(t, ts, token, "/v1/updates/"+app+"/apk")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("APK status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); got != "application/vnd.android.package-archive" {
				t.Fatalf("APK Content-Type = %q", got)
			}
			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Fatalf("APK Cache-Control = %q", got)
			}
			if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, fmt.Sprintf("agent-watch-%s-5.apk", app)) {
				t.Fatalf("APK Content-Disposition = %q", got)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("APK X-Content-Type-Options = %q", got)
			}
			body := make([]byte, 32)
			n, _ := resp.Body.Read(body)
			resp.Body.Close()
			if string(body[:n]) != "apk bytes" {
				t.Fatalf("APK body = %q", body[:n])
			}
		})
	}
}

func TestUpdates_AuthRequired(t *testing.T) {
	_, ts, dir, _ := updateTestServer(t)
	publishUpdate(t, dir, model.UpdateAppPhone, 5, []byte("apk"))

	resp := getUpdate(t, ts, "", "/v1/updates/phone")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestUpdates_InvalidApp(t *testing.T) {
	_, ts, _, token := updateTestServer(t)

	resp := getUpdate(t, ts, token, "/v1/updates/android")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := decodeError(t, resp)
	if body.Error.Code != string(model.ErrInvalidRequest) {
		t.Fatalf("error code = %q, want invalid_request", body.Error.Code)
	}
}

func TestUpdates_NotAvailable(t *testing.T) {
	_, ts, _, token := updateTestServer(t)

	for _, path := range []string{"/v1/updates/phone", "/v1/updates/watch/apk"} {
		resp := getUpdate(t, ts, token, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.StatusCode)
		}
		body := decodeError(t, resp)
		if body.Error.Code != string(model.ErrUpdateUnavailable) {
			t.Fatalf("%s error code = %q, want update_not_available", path, body.Error.Code)
		}
	}
}

func TestUpdates_MissingAPKIsNotAvailable(t *testing.T) {
	_, ts, dir, token := updateTestServer(t)
	publishUpdate(t, dir, model.UpdateAppPhone, 5, []byte("apk"))
	if err := os.Remove(filepath.Join(dir, "phone-5.apk")); err != nil {
		t.Fatal(err)
	}

	resp := getUpdate(t, ts, token, "/v1/updates/phone")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error.Code != string(model.ErrUpdateUnavailable) {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestUpdates_UnusableMetadata(t *testing.T) {
	apk := []byte("apk bytes")
	sum := sha256.Sum256(apk)
	good := model.UpdateInfo{
		App:         model.UpdateAppPhone,
		VersionCode: 5,
		VersionName: "0.4.0",
		SHA256:      hex.EncodeToString(sum[:]),
		Size:        int64(len(apk)),
	}

	tests := []struct {
		name     string
		manifest updateManifest
	}{
		{name: "no version", manifest: updateManifest{UpdateInfo: model.UpdateInfo{App: "phone", VersionName: "0.4.0", SHA256: good.SHA256, Size: good.Size}, APK: "phone-5.apk"}},
		{name: "bad hash", manifest: updateManifest{UpdateInfo: model.UpdateInfo{App: "phone", VersionCode: 5, VersionName: "0.4.0", SHA256: "not hex", Size: good.Size}, APK: "phone-5.apk"}},
		{name: "zero size", manifest: updateManifest{UpdateInfo: model.UpdateInfo{App: "phone", VersionCode: 5, VersionName: "0.4.0", SHA256: good.SHA256}, APK: "phone-5.apk"}},
		{name: "traversal", manifest: updateManifest{UpdateInfo: good, APK: "../phone-5.apk"}},
		{name: "wrong suffix", manifest: updateManifest{UpdateInfo: good, APK: "phone-5.json"}},
		{name: "other app name", manifest: updateManifest{UpdateInfo: good, APK: "watch-5.apk"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ts, dir, token := updateTestServer(t)
			if err := os.WriteFile(filepath.Join(dir, "phone-5.apk"), apk, 0o600); err != nil {
				t.Fatal(err)
			}
			writeManifestAs(t, dir, "phone", tc.manifest)

			resp := getUpdate(t, ts, token, "/v1/updates/phone")
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", resp.StatusCode)
			}
			body := decodeError(t, resp)
			if body.Error.Code != string(model.ErrInternal) {
				t.Fatalf("error code = %q, want internal", body.Error.Code)
			}
		})
	}
}

func TestUpdates_ManifestForAnotherApp(t *testing.T) {
	_, ts, dir, token := updateTestServer(t)
	apk := []byte("apk bytes")
	sum := sha256.Sum256(apk)
	if err := os.WriteFile(filepath.Join(dir, "phone-5.apk"), apk, 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifestAs(t, dir, "phone", updateManifest{
		UpdateInfo: model.UpdateInfo{
			App:         model.UpdateAppWatch,
			VersionCode: 5,
			VersionName: "0.4.0",
			SHA256:      hex.EncodeToString(sum[:]),
			Size:        int64(len(apk)),
		},
		APK: "phone-5.apk",
	})

	resp := getUpdate(t, ts, token, "/v1/updates/phone")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error.Code != string(model.ErrInternal) {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestUpdates_SizeMismatch(t *testing.T) {
	_, ts, dir, token := updateTestServer(t)
	info := publishUpdate(t, dir, model.UpdateAppPhone, 5, []byte("apk bytes"))
	info.Size++
	writeManifest(t, dir, updateManifest{UpdateInfo: info, APK: "phone-5.apk"})

	resp := getUpdate(t, ts, token, "/v1/updates/phone/apk")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error.Code != string(model.ErrInternal) {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestUpdates_Head(t *testing.T) {
	_, ts, dir, token := updateTestServer(t)
	publishUpdate(t, dir, model.UpdateAppPhone, 5, []byte("apk bytes"))

	req, _ := http.NewRequest(http.MethodHead, ts.URL+"/v1/updates/phone/apk", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.ContentLength != int64(len("apk bytes")) {
		t.Fatalf("Content-Length = %d", resp.ContentLength)
	}
}
