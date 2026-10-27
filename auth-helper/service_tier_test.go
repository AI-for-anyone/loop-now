package authhelper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceTierEntryMustMatchEmbeddedAdapter(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "private")
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	_, extension, err := Install(filepath.Join(parent, "helpers"))
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(filepath.Dir(extension), "codex-service-tier.mjs")
	if !ValidateServiceTierEntry(entry) {
		t.Fatal("installed adapter rejected")
	}
	if err = os.WriteFile(entry, []byte("export default ()=>{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateServiceTierEntry(entry) {
		t.Fatal("modified adapter admitted")
	}
}
