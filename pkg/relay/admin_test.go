package relay

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setTestServiceEnv(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := ServiceEnvFile
	ServiceEnvFile = path
	t.Cleanup(func() { ServiceEnvFile = old })
}

func TestAdmin_OfflineParity(t *testing.T) {
	dataDir := t.TempDir()
	envToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	setTestServiceEnv(t, "AW_HOST_TOKEN="+envToken+"\nAW_HOST_ID=main\nAW_HOST_NAME=Mac\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Initial list: should include the env host with Env: true, Online: false.
	hosts, err := AdminListHosts(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListHosts: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("AdminListHosts len = %d, want 1", len(hosts))
	}
	if h := hosts[0]; h.ID != "main" || h.Name != "Mac" || !h.Env || h.Online {
		t.Fatalf("AdminListHosts env host = %+v", h)
	}

	// 2. AddHost with env host ID: returns ErrHostIsEnv.
	if _, _, _, err := AdminAddHost(ctx, dataDir, "main", "Mac Duplicate"); !errors.Is(err, ErrHostIsEnv) {
		t.Fatalf("AdminAddHost(main) err = %v, want ErrHostIsEnv", err)
	}

	// 3. AddHost with new host: succeeds offline (viaRelay == false).
	info, tok, viaRelay, err := AdminAddHost(ctx, dataDir, "linux", "Linux Box")
	if err != nil {
		t.Fatalf("AdminAddHost(linux): %v", err)
	}
	if viaRelay {
		t.Fatalf("AdminAddHost viaRelay = true, want false")
	}
	if tok == "" || info.ID != "linux" || info.Name != "Linux Box" {
		t.Fatalf("AdminAddHost info = %+v, tok = %q", info, tok)
	}

	// 4. ListHosts: contains both "linux" and "main", sorted by ID.
	hosts, err = AdminListHosts(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListHosts: %v", err)
	}
	if len(hosts) != 2 || hosts[0].ID != "linux" || hosts[1].ID != "main" {
		t.Fatalf("AdminListHosts after add = %+v", hosts)
	}

	// 5. AddHost duplicate: returns ErrHostExists.
	if _, _, _, err := AdminAddHost(ctx, dataDir, "linux", "Linux Duplicate"); !errors.Is(err, ErrHostExists) {
		t.Fatalf("AdminAddHost duplicate err = %v, want ErrHostExists", err)
	}

	// 6. RevokeHost with env host ID: returns ErrHostIsEnv.
	if _, err := AdminRevokeHost(ctx, dataDir, "main"); !errors.Is(err, ErrHostIsEnv) {
		t.Fatalf("AdminRevokeHost(main) err = %v, want ErrHostIsEnv", err)
	}

	// 7. RevokeHost nonexistent: returns ErrHostNotFound.
	if _, err := AdminRevokeHost(ctx, dataDir, "unknown"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("AdminRevokeHost(unknown) err = %v, want ErrHostNotFound", err)
	}

	// 8. RevokeHost "linux": succeeds offline (viaRelay == false).
	viaRelay, err = AdminRevokeHost(ctx, dataDir, "linux")
	if err != nil {
		t.Fatalf("AdminRevokeHost(linux): %v", err)
	}
	if viaRelay {
		t.Fatalf("AdminRevokeHost viaRelay = true, want false")
	}

	// 9. ListHosts: only "main" remains.
	hosts, err = AdminListHosts(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "main" {
		t.Fatalf("AdminListHosts after revoke = %+v", hosts)
	}
}

func TestAdmin_OnlineOfflineParity(t *testing.T) {
	dataDir := t.TempDir()
	envToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	setTestServiceEnv(t, "AW_HOST_TOKEN="+envToken+"\nAW_HOST_ID=main\nAW_HOST_NAME=Mac\n")

	cfg := &Config{
		DataDir:   dataDir,
		HostToken: envToken,
		HostID:    "main",
		HostName:  "Mac",
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = server.Serve(srvCtx, ln)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Online ListHosts via Admin socket.
	hosts, err := AdminListHosts(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListHosts online: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "main" || !hosts[0].Env {
		t.Fatalf("AdminListHosts online = %+v", hosts)
	}

	// 2. Online AddHost: succeeds (viaRelay == true).
	info, tok, viaRelay, err := AdminAddHost(ctx, dataDir, "linux", "Linux Box")
	if err != nil {
		t.Fatalf("AdminAddHost online: %v", err)
	}
	if !viaRelay {
		t.Fatalf("AdminAddHost viaRelay = false, want true")
	}
	if tok == "" || info.ID != "linux" {
		t.Fatalf("AdminAddHost info = %+v", info)
	}

	// Verify server state was updated in memory.
	allHosts := server.State().Hosts()
	if len(allHosts) != 2 {
		t.Fatalf("server.State().Hosts() len = %d, want 2", len(allHosts))
	}

	// 3. Online AddHost with env host ID: returns ErrHostIsEnv.
	if _, _, _, err := AdminAddHost(ctx, dataDir, "main", "Duplicate"); !errors.Is(err, ErrHostIsEnv) {
		t.Fatalf("AdminAddHost(main) online err = %v, want ErrHostIsEnv", err)
	}

	// 4. Online AddHost duplicate: returns ErrHostExists.
	if _, _, _, err := AdminAddHost(ctx, dataDir, "linux", "Duplicate"); !errors.Is(err, ErrHostExists) {
		t.Fatalf("AdminAddHost(linux) duplicate online err = %v, want ErrHostExists", err)
	}

	// 5. Online RevokeHost env host: returns ErrHostIsEnv.
	if _, err := AdminRevokeHost(ctx, dataDir, "main"); !errors.Is(err, ErrHostIsEnv) {
		t.Fatalf("AdminRevokeHost(main) online err = %v, want ErrHostIsEnv", err)
	}

	// 6. Online RevokeHost nonexistent: returns ErrHostNotFound.
	if _, err := AdminRevokeHost(ctx, dataDir, "unknown"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("AdminRevokeHost(unknown) online err = %v, want ErrHostNotFound", err)
	}

	// 7. Online RevokeHost "linux": succeeds (viaRelay == true).
	viaRelay, err = AdminRevokeHost(ctx, dataDir, "linux")
	if err != nil {
		t.Fatalf("AdminRevokeHost online: %v", err)
	}
	if !viaRelay {
		t.Fatalf("AdminRevokeHost viaRelay = false, want true")
	}

	// Verify server state was updated.
	allHosts = server.State().Hosts()
	if len(allHosts) != 1 || allHosts[0].ID != "main" {
		t.Fatalf("server.State().Hosts() after revoke = %+v", allHosts)
	}

	// 8. Shut down server and verify offline works on the same dataDir.
	srvCancel()
	<-srvDone

	hosts, err = AdminListHosts(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListHosts after server stop: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "main" {
		t.Fatalf("AdminListHosts after server stop = %+v", hosts)
	}
}

func TestAdmin_EnvHostShadowing(t *testing.T) {
	dataDir := t.TempDir()
	envToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	setTestServiceEnv(t, "AW_HOST_TOKEN="+envToken+"\nAW_HOST_ID=main\nAW_HOST_NAME=Mac\n")

	// Pre-populate store with a host whose token hash matches AW_HOST_TOKEN.
	st, err := OpenStore(dataDir, "main")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := st.AddHost("stored-mac", "Stored Mac", Sha256Hex(envToken)); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Offline list: AW_HOST_TOKEN is shadowed, so "main" is NOT included.
	hosts, err := AdminListHosts(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "stored-mac" || hosts[0].Env {
		t.Fatalf("AdminListHosts shadowed offline = %+v", hosts)
	}

	// 2. Online: NewServer with the same store.
	cfg := &Config{
		DataDir:   dataDir,
		HostToken: envToken,
		HostID:    "main",
		HostName:  "Mac",
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	onlineHosts, err := server.Admin().ListHosts(ctx)
	if err != nil {
		t.Fatalf("server.Admin().ListHosts: %v", err)
	}
	if len(onlineHosts) != 1 || onlineHosts[0].ID != "stored-mac" || onlineHosts[0].Env {
		t.Fatalf("server.Admin().ListHosts shadowed online = %+v", onlineHosts)
	}
}

func TestAdmin_DevicesParity(t *testing.T) {
	dataDir := t.TempDir()
	devToken, _ := GenerateDeviceToken()

	st, err := OpenStore(dataDir, "main")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	dev, err := st.AddDevice("My Watch", Sha256Hex(devToken))
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Offline ListDevices.
	devs, err := AdminListDevices(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListDevices: %v", err)
	}
	if len(devs) != 1 || devs[0].ID != dev.ID || devs[0].Name != "My Watch" {
		t.Fatalf("AdminListDevices = %+v", devs)
	}

	// 2. Offline RevokeDevice unknown.
	if _, err := AdminRevokeDevice(ctx, dataDir, "nonexistent"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("AdminRevokeDevice(nonexistent) err = %v, want ErrDeviceNotFound", err)
	}

	// 3. Offline RevokeDevice known.
	viaRelay, err := AdminRevokeDevice(ctx, dataDir, dev.ID)
	if err != nil {
		t.Fatalf("AdminRevokeDevice: %v", err)
	}
	if viaRelay {
		t.Fatalf("AdminRevokeDevice viaRelay = true, want false")
	}

	// 4. Offline ListDevices after revoke: empty.
	devs, err = AdminListDevices(ctx, dataDir)
	if err != nil {
		t.Fatalf("AdminListDevices: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("AdminListDevices after revoke = %+v", devs)
	}
}
