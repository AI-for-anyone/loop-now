package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"loop-now/internal/server/clicredential"
)

// Synthetic file content tests installation/integrity only; never executes a
// native CLI or claims that these fixtures prove the official source build.
func larkAssetFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "bin"), 0700); e != nil {
		t.Fatal(e)
	}
	hashes := map[string]string{}
	for _, name := range []string{"lark-cli", "lark-cli-launcher", "lark-cli-sandbox-probe"} {
		b := []byte("synthetic:" + name)
		d := sha256.Sum256(b)
		hashes[name] = hex.EncodeToString(d[:])
		if e := os.WriteFile(filepath.Join(root, name), b, 0700); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := os.ReadFile(filepath.Join(root, "lark-cli-launcher"))
	os.WriteFile(filepath.Join(root, "bin/lark-cli"), b, 0700)
	os.WriteFile(filepath.Join(root, "LICENSE"), []byte("synthetic-license"), 0600)
	m := map[string]any{"artifactVersion": clicredential.LarkUserVariant, "sourceCommit": larkSourceCommit, "sourceSHA256": larkSourceSHA, "platform": runtime.GOOS + "-" + runtime.GOARCH, "officialRelease": false, "sourceModified": false, "buildTags": []string{"authsidecar"}, "binarySHA256": hashes["lark-cli"], "sandboxProbe": map[string]string{"file": "lark-cli-sandbox-probe", "sha256": hashes["lark-cli-sandbox-probe"], "protocol": "loop-now-lark-sandbox-probe-v2"}, "launcher": map[string]string{"file": "lark-cli-launcher", "entryFile": "bin/lark-cli", "sha256": hashes["lark-cli-launcher"], "protocol": "loop-now-lark-exec-v1"}}
	raw, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0600)
	d := sha256.Sum256(raw)
	return root, hex.EncodeToString(d[:])
}

func TestLarkAssetInstallPinAndImmutableCache(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Lark variant currently only supports darwin-arm64")
	}
	source, pin := larkAssetFixture(t)
	work := t.TempDir()
	cfg, e := prepareLarkAssets(context.Background(), source, work, pin)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Binary == filepath.Join(source, "lark-cli") || filepath.Base(filepath.Dir(cfg.LauncherBinary)) != "bin" {
		t.Fatal("not installed in independent controlled namespace")
	}
	second, e := prepareLarkAssets(context.Background(), source, work, pin)
	if e != nil || second.Binary != cfg.Binary {
		t.Fatal("unchanged package failed idempotent validation")
	}
	if _, e = prepareLarkAssets(context.Background(), source, work, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); e == nil {
		t.Fatal("untrusted manifest accepted")
	}
	if e = os.WriteFile(cfg.Binary, []byte("tampered"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = prepareLarkAssets(context.Background(), source, work, pin); e == nil {
		t.Fatal("tampered cache silently accepted or replaced")
	}
	got, _ := os.ReadFile(cfg.Binary)
	if string(got) != "tampered" {
		t.Fatal("invalid cache was silently overwritten")
	}
}

func TestLarkAssetInstallRejectsSourceSymlinkAndWrongProtocol(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Lark variant currently only supports darwin-arm64")
	}
	source, pin := larkAssetFixture(t)
	if e := os.Remove(filepath.Join(source, "bin/lark-cli")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("../lark-cli-launcher", filepath.Join(source, "bin/lark-cli")); e != nil {
		t.Fatal(e)
	}
	if _, e := prepareLarkAssets(context.Background(), source, t.TempDir(), pin); e == nil {
		t.Fatal("package symlink accepted")
	}
	source, _ = larkAssetFixture(t)
	raw, _ := os.ReadFile(filepath.Join(source, "manifest.json"))
	var m map[string]any
	json.Unmarshal(raw, &m)
	m["launcher"].(map[string]any)["protocol"] = "loop-now-lark-launcher-v1"
	raw, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(source, "manifest.json"), raw, 0600)
	d := sha256.Sum256(raw)
	if _, e := prepareLarkAssets(context.Background(), source, t.TempDir(), hex.EncodeToString(d[:])); e == nil {
		t.Fatal("old writable-descriptor bootstrap accepted")
	}
}

func TestLarkAssetInstallDoesNotFollowWorkRootEscape(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Lark variant currently only supports darwin-arm64")
	}
	source, pin := larkAssetFixture(t)
	work, outside := t.TempDir(), t.TempDir()
	if e := os.Symlink(outside, filepath.Join(work, "tools")); e != nil {
		t.Fatal(e)
	}
	if _, e := prepareLarkAssets(context.Background(), source, work, pin); e == nil {
		t.Fatal("installer followed outside working root")
	}
	entries, e := os.ReadDir(outside)
	if e != nil || len(entries) != 0 {
		t.Fatal("installer wrote outside working root")
	}
}
