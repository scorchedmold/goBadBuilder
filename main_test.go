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

func TestAuroraLaunchPathFindsAuroraUnderApps(t *testing.T) {
	targetRoot := t.TempDir()
	writeTestFile(t, filepath.Join(targetRoot, "Apps", "Aurora", "Aurora.xex"), "aurora")

	path, err := auroraLaunchPath(targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if path != `Usb:\Apps\Aurora\Aurora.xex` {
		t.Fatalf("unexpected Aurora path: %q", path)
	}
}

func TestUpdateLaunchINIWritesDashLaunchSettings(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "launch.ini")
	if err := os.WriteFile(path, []byte("[Paths]\nDefault = Usb:\\Old\\default.xex\n[Settings]\ncontpatch = false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := updateLaunchINI(path, launchINISettings{
		AuroraPath: `Usb:\Apps\Aurora\Aurora.xex`,
		ContPatch:  true,
		XBLAPatch:  true,
		LicPatch:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := "[Paths]\n" +
		"Default = Usb:\\Apps\\Aurora\\Aurora.xex\n" +
		"[Settings]\n" +
		"contpatch = true\n" +
		"xblapatch = true\n" +
		"licpatch = true\n"
	if string(contents) != expected {
		t.Fatalf("unexpected launch.ini contents:\n%s", contents)
	}
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
