package modelinstall

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDialValidatedFallsBackInDNSOrder(t *testing.T) {
	first, second := netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("8.8.8.8")
	var tried []string
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		tried = append(tried, network+" "+address)
		if len(tried) == 1 {
			return nil, errors.New("first address failed")
		}
		return &fakeConn{}, nil
	}
	conn, err := dialValidated(context.Background(), dial, "tcp", "443", []netip.Addr{first, second})
	if err != nil || conn == nil || len(tried) != 2 {
		t.Fatalf("fallback failed: conn=%v err=%v tried=%v", conn, err, tried)
	}
	_ = conn.Close()
}

type fakeConn struct{}

func (*fakeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*fakeConn) Write(p []byte) (int, error)      { return len(p), nil }
func (*fakeConn) Close() error                     { return nil }
func (*fakeConn) LocalAddr() net.Addr              { return dummyAddr("local") }
func (*fakeConn) RemoteAddr() net.Addr             { return dummyAddr("remote") }
func (*fakeConn) SetDeadline(time.Time) error      { return nil }
func (*fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (*fakeConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

func TestRecoveryMarksRunningOperationFailedAndRemovesOnlyItsStage(t *testing.T) {
	workspace, models := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, "model-installer")
	if err := os.MkdirAll(filepath.Join(models, "bundles"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	stage := filepath.Join(models, "bundles", ".bundle-b-"+id+".staging")
	candidate := filepath.Join(models, "bundles", "bundle-b-candidate-"+id[:12])
	published := filepath.Join(models, "bundles", "bundle-b-published")
	if err := os.Mkdir(stage, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(candidate, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(published, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(Operation{ID: id, Status: "running", Phase: Downloading})
	if err := os.WriteFile(filepath.Join(root, "latest-operation.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New(true, workspace, models, "revision", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	op := m.Latest()
	if op.Status != "failed" || op.Phase != Failed || op.ErrorCode != "INTERRUPTED" {
		t.Fatalf("unexpected recovery: %#v", op)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage remains: %v", err)
	}
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Fatalf("unpersisted candidate remains: %v", err)
	}
	if _, err := os.Stat(published); err != nil {
		t.Fatalf("published bundle removed: %v", err)
	}
}

func TestRemoveOperationArtifactsRemovesUnpersistedCandidateForTerminalOperations(t *testing.T) {
	models := t.TempDir()
	if err := os.MkdirAll(filepath.Join(models, "bundles"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			candidate := filepath.Join(models, "bundles", "bundle-b-"+status+"-"+id[:12])
			if err := os.Mkdir(candidate, 0o750); err != nil {
				t.Fatal(err)
			}
			m := &Manager{models: models}
			m.removeOperationArtifacts(Operation{ID: id, Status: status})
			if _, err := os.Stat(candidate); !os.IsNotExist(err) {
				t.Fatalf("unpersisted candidate remains: %v", err)
			}
		})
	}
}

func TestCancelRejectsAfterPublicationCommit(t *testing.T) {
	cancelled := false
	m := &Manager{cancel: func() { cancelled = true }, publicationCommitted: true, op: Operation{ID: "0123456789abcdef0123456789abcdef"}}
	if err := m.Cancel(m.op.ID); err == nil {
		t.Fatal("cancellation succeeded after publication commit")
	}
	if cancelled {
		t.Fatal("committed operation context was cancelled")
	}
}

func TestPublicIPRejectsReservedAndMappedAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "::1", "::ffff:8.8.8.8"} {
		if publicIP(net.ParseIP(raw)) {
			t.Errorf("accepted reserved %s", raw)
		}
	}
	if !publicIP(net.IP{8, 8, 8, 8}) {
		t.Fatal("rejected public IPv4")
	}
}

func TestOperationIDValidation(t *testing.T) {
	if !validOperationID("0123456789abcdef0123456789abcdef") || validOperationID("ABCDEF0123456789abcdef012345678") {
		t.Fatal("operation ID validation mismatch")
	}
}

func TestConverterStableArtifactPathUsesModelsRoot(t *testing.T) {
	models := t.TempDir()
	version := filepath.Join(models, "bundles", "bundle-b-candidate")
	path := filepath.Join(version, "clip", "vit-b-32.pt")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	x := provenance{}
	x.Clip.Artifact = artifact{Path: filepath.Join(models, "bundle-b", "clip", "vit-b-32.pt"), Bytes: 0}
	if err := verifyReportedArtifact(version, "bundle-b/clip/vit-b-32.pt", x, false); err != nil {
		t.Fatalf("converter-shaped stable path rejected: %v", err)
	}
	x.Clip.Artifact.Path = filepath.Join(filepath.Dir(models), "bundle-b", "clip", "vit-b-32.pt")
	if err := verifyReportedArtifact(version, "bundle-b/clip/vit-b-32.pt", x, false); err == nil {
		t.Fatal("path outside models root accepted")
	}
}

func TestConverterStableArtifactPathThroughCandidateSymlink(t *testing.T) {
	models := t.TempDir()
	version := filepath.Join(models, "bundles", "bundle-b-candidate")
	path := filepath.Join(version, "clip", "vit-b-32.pt")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	check := filepath.Join(models, ".bundle-b-candidate-test")
	if err := os.Symlink(filepath.Join("bundles", "bundle-b-candidate"), check); err != nil {
		t.Fatal(err)
	}
	x := provenance{}
	x.Clip.Artifact = artifact{Path: filepath.Join(models, "bundle-b", "clip", "vit-b-32.pt"), Bytes: 0}
	if err := verifyReportedArtifact(check, "bundle-b/clip/vit-b-32.pt", x, false); err != nil {
		t.Fatalf("candidate symlink rejected converter-shaped stable path: %v", err)
	}
}

func TestRecoveryRestoresPriorWhenCandidateFailsVerification(t *testing.T) {
	workspace, models := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, "model-installer")
	if err := os.MkdirAll(filepath.Join(models, "bundles"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	prior, candidate := "bundles/prior", "bundles/candidate-"+id[:12]
	if err := os.Mkdir(filepath.Join(models, prior), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(models, candidate), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(candidate, filepath.Join(models, "bundle-b")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(Operation{ID: id, Status: "running", Phase: Verifying, CandidateTarget: candidate, PriorTarget: prior})
	if err := os.WriteFile(filepath.Join(root, "latest-operation.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(true, workspace, models, "revision", testLogger()); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(models, "bundle-b"))
	if err != nil || target != prior {
		t.Fatalf("prior target not restored: target=%q err=%v", target, err)
	}
	if _, err := os.Stat(filepath.Join(models, candidate)); !os.IsNotExist(err) {
		t.Fatalf("invalid candidate remains: %v", err)
	}
}

func TestRecoverySkipsLockedOperation(t *testing.T) {
	workspace, models := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, "model-installer")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	first, err := New(true, workspace, models, "revision", testLogger())
	if err != nil || !first.Enabled() {
		t.Fatalf("first manager unavailable: %v", err)
	}
	lock, err := os.OpenFile(filepath.Join(root, "operation.lock"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	m, err := New(true, workspace, models, "revision", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if m.Enabled() {
		t.Fatal("locked manager reported enabled")
	}
	if _, err := m.Start(time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unexpected start error: %v", err)
	}
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
