package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallABadAvatarMergesContentUnderContentFolder(t *testing.T) {
	tmp := t.TempDir()
	extractDir := filepath.Join(tmp, "Extract")
	targetRoot := filepath.Join(tmp, "USB")
	a := &app{extractDir: extractDir}

	writeTestFile(t, filepath.Join(extractDir, "ABadAvatar", "ABadAvatar-publicbeta1.0", "BadUpdatePayload", "avatar.xex"), "avatar")
	writeTestFile(t, filepath.Join(extractDir, "ABadAvatar", "ABadAvatar-publicbeta1.0", "Content", "E0002FF78DFBDE7B", "profile.bin"), "profile")
	writeTestFile(t, filepath.Join(targetRoot, "BadUpdatePayload", "default.xex"), "default")
	writeTestFile(t, filepath.Join(targetRoot, "BadUpdatePayload", "BadStorage.xex.dll"), "storage")
	writeTestFile(t, filepath.Join(targetRoot, "BadUpdatePayload", "old.bin"), "old")
	writeTestFile(t, filepath.Join(targetRoot, filepath.FromSlash(contentFolder), "5841122D", "old-title.bin"), "old title")

	if err := a.installABadAvatar(targetRoot); err != nil {
		t.Fatal(err)
	}

	assertFileExists(t, filepath.Join(targetRoot, "BadUpdatePayload", "default.xex"))
	assertFileExists(t, filepath.Join(targetRoot, "BadUpdatePayload", "BadStorage.xex.dll"))
	assertFileExists(t, filepath.Join(targetRoot, "BadUpdatePayload", "avatar.xex"))
	assertFileMissing(t, filepath.Join(targetRoot, "BadUpdatePayload", "old.bin"))
	assertFileMissing(t, filepath.Join(targetRoot, filepath.FromSlash(contentFolder), "5841122D"))
	assertFileExists(t, filepath.Join(targetRoot, "Content", "E0002FF78DFBDE7B", "profile.bin"))
	assertFileMissing(t, filepath.Join(targetRoot, "E0002FF78DFBDE7B", "profile.bin"))
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("expected %s to be a file", path)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		t.Fatalf("expected %s to be missing", path)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
