package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	workDirName      = "Work"
	downloadDirName  = "Download"
	extractDirName   = "Extract"
	contentFolder    = "Content/0000000000000000"
	defaultUserAgent = "goBadBuilder"
)

type app struct {
	in                *bufio.Reader
	httpClient        *http.Client
	workDir           string
	downloadDir       string
	extractDir        string
	skipPatchingKnown bool
	skipPatching      bool
}

type downloadItem struct {
	Name string
	URL  string
}

type archiveItem struct {
	Name string
	Path string
}

type homebrewApp struct {
	Name       string
	Folder     string
	EntryPoint string
}

type release struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func main() {
	var workDirFlag string
	var targetFlag string
	var defaultAppFlag string

	flag.StringVar(&workDirFlag, "work-dir", workDirName, "working directory for downloads and extracted files")
	flag.StringVar(&targetFlag, "target", "", "USB root path to write to")
	flag.StringVar(&defaultAppFlag, "default-app", "", "BadUpdate payload to launch: FreeMyXe or XeUnshackle")
	flag.Parse()

	absWorkDir, err := filepath.Abs(workDirFlag)
	if err != nil {
		exitErr(err)
	}

	a := &app{
		in:          bufio.NewReader(os.Stdin),
		httpClient:  &http.Client{Timeout: 30 * time.Minute},
		workDir:     absWorkDir,
		downloadDir: filepath.Join(absWorkDir, downloadDirName),
		extractDir:  filepath.Join(absWorkDir, extractDirName),
	}

	if err := a.run(targetFlag, defaultAppFlag); err != nil {
		exitErr(err)
	}
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
	os.Exit(1)
}

func (a *app) run(targetFlag string, defaultAppFlag string) error {
	printBanner()

	fmt.Println("This Go version does not format drives.")
	fmt.Println("Format your USB drive as FAT32 first, then choose its mounted root folder here.")
	fmt.Println()

	targetRoot, err := a.targetRoot(targetFlag)
	if err != nil {
		return err
	}

	defaultApp, err := a.defaultApp(defaultAppFlag)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(a.downloadDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(a.extractDir, 0755); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Checking required release assets...")
	items, err := a.requiredDownloads(context.Background())
	if err != nil {
		return err
	}

	archives, err := a.prepareArchives(context.Background(), items)
	if err != nil {
		return err
	}

	if err := a.extractArchives(archives); err != nil {
		return err
	}

	if err := a.prepareUSB(targetRoot, defaultApp); err != nil {
		return err
	}

	addHomebrew, err := a.askYesNo("Add homebrew apps?", false)
	if err != nil {
		return err
	}
	homebrewCount := 1
	if addHomebrew {
		homebrewApps, err := a.manageHomebrew()
		if err != nil {
			return err
		}
		if len(homebrewApps) > 0 {
			if err := a.copyHomebrew(targetRoot, homebrewApps); err != nil {
				return err
			}
			homebrewCount += len(homebrewApps)
			fmt.Printf("Added %d homebrew app(s).\n", len(homebrewApps))
		}
	}

	if err := appendInfo(targetRoot, fmt.Sprintf("-  %d homebrew app(s) added (including Simple 360 NAND Flasher)\n", homebrewCount)); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Your USB drive is ready for manual testing.")
	return nil
}

func printBanner() {
	fmt.Println("BadBuilder")
	fmt.Println("Xbox 360 BadUpdate USB Builder - Go CLI")
	fmt.Println("--------------------------------------")
}

func (a *app) targetRoot(targetFlag string) (string, error) {
	targetRoot := strings.TrimSpace(targetFlag)
	var err error

	for targetRoot == "" {
		targetRoot, err = a.ask("USB root path")
		if err != nil {
			return "", err
		}
		targetRoot = cleanInputPath(targetRoot)
	}

	targetRoot, err = filepath.Abs(cleanInputPath(targetRoot))
	if err != nil {
		return "", err
	}

	info, err := os.Stat(targetRoot)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", targetRoot)
	}

	fmt.Printf("Target USB root: %s\n", targetRoot)
	ok, err := a.askYesNo("Write BadUpdate files to this directory?", false)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("target directory was not confirmed")
	}
	return targetRoot, nil
}

func (a *app) defaultApp(defaultAppFlag string) (string, error) {
	choice := strings.TrimSpace(defaultAppFlag)
	switch strings.ToLower(choice) {
	case "":
	case "freemyxe":
		return "FreeMyXe", nil
	case "xeunshackle":
		return "XeUnshackle", nil
	default:
		return "", fmt.Errorf("unknown default app %q", choice)
	}

	selected, err := a.choose("Which program should BadUpdate launch?", []string{"FreeMyXe", "XeUnshackle"})
	if err != nil {
		return "", err
	}
	return selected, nil
}

func (a *app) requiredDownloads(ctx context.Context) ([]downloadItem, error) {
	items := []downloadItem{
		{Name: "XeXmenu", URL: "https://github.com/Pdawg-bytes/BadBuilder/releases/download/v0.10a/MenuData.7z"},
		{Name: "Rock Band Blitz", URL: "https://github.com/Pdawg-bytes/BadBuilder/releases/download/v0.10a/GameData.zip"},
		{Name: "Simple 360 NAND Flasher", URL: "https://github.com/Pdawg-bytes/BadBuilder/releases/download/v0.10a/Flasher.7z"},
	}

	repos := []string{
		"grimdoomer/Xbox360BadUpdate",
		"Byrom90/XeUnshackle",
		"FreeMyXe/FreeMyXe",
	}

	for _, repo := range repos {
		assets, err := a.latestReleaseAssets(ctx, repo)
		if err != nil {
			return nil, err
		}
		for _, asset := range assets {
			items = append(items, downloadItem{
				Name: friendlyAssetName(asset.Name),
				URL:  asset.BrowserDownloadURL,
			})
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].URL < items[j].URL
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (a *app) latestReleaseAssets(ctx context.Context, repo string) ([]releaseAsset, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release for %s: %w", repo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("fetch latest release for %s: HTTP %s: %s", repo, resp.Status, strings.TrimSpace(string(body)))
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return rel.Assets, nil
}

func friendlyAssetName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "free"):
		return "FreeMyXe"
	case strings.Contains(lower, "tools"):
		return "BadUpdate Tools"
	case strings.Contains(lower, "badupdate"):
		return "BadUpdate"
	case strings.Contains(lower, "xeunshackle"):
		return "XeUnshackle"
	default:
		ext := filepath.Ext(name)
		return strings.TrimSuffix(name, ext)
	}
}

func (a *app) prepareArchives(ctx context.Context, items []downloadItem) ([]archiveItem, error) {
	var archives []archiveItem

	for _, item := range items {
		filename := filenameFromURL(item.URL)
		dest := filepath.Join(a.downloadDir, filename)

		if fileExists(dest) {
			useExisting, err := a.askYesNo(fmt.Sprintf("Use existing %s archive at %s?", item.Name, dest), true)
			if err != nil {
				return nil, err
			}
			if useExisting {
				archives = append(archives, archiveItem{Name: item.Name, Path: dest})
				continue
			}
		}

		download, err := a.askYesNo(fmt.Sprintf("Download %s?", item.Name), true)
		if err != nil {
			return nil, err
		}
		if download {
			if err := a.downloadFile(ctx, item.URL, dest); err != nil {
				return nil, err
			}
			archives = append(archives, archiveItem{Name: item.Name, Path: dest})
			continue
		}

		localPath, err := a.askExistingFile(fmt.Sprintf("Path to %s archive", item.Name))
		if err != nil {
			return nil, err
		}
		if err := copyFile(localPath, dest); err != nil {
			return nil, err
		}
		archives = append(archives, archiveItem{Name: item.Name, Path: dest})
	}

	return archives, nil
}

func filenameFromURL(rawURL string) string {
	trimmed := strings.TrimRight(rawURL, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx == -1 {
		return trimmed
	}
	return trimmed[idx+1:]
}

func (a *app) downloadFile(ctx context.Context, url string, dest string) error {
	fmt.Printf("Downloading %s\n", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}

	tmp := dest + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	var written int64
	buf := make([]byte, 1024*128)
	lastPrint := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			written += int64(n)
			if time.Since(lastPrint) > time.Second {
				printDownloadProgress(written, resp.ContentLength)
				lastPrint = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return readErr
		}
	}

	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}

	printDownloadProgress(written, resp.ContentLength)
	fmt.Println()
	return nil
}

func printDownloadProgress(written int64, total int64) {
	if total > 0 {
		fmt.Printf("\r  %.1f / %.1f MB", bytesToMB(written), bytesToMB(total))
		return
	}
	fmt.Printf("\r  %.1f MB", bytesToMB(written))
}

func bytesToMB(bytes int64) float64 {
	return float64(bytes) / 1024 / 1024
}

func (a *app) extractArchives(archives []archiveItem) error {
	sort.SliceStable(archives, func(i, j int) bool {
		return len(archives[i].Name) > len(archives[j].Name)
	})

	for _, archive := range archives {
		dest := filepath.Join(a.extractDir, archive.Name)
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		if err := os.MkdirAll(dest, 0755); err != nil {
			return err
		}

		fmt.Printf("Extracting %s\n", archive.Name)
		if err := extractArchive(archive.Path, dest); err != nil {
			return fmt.Errorf("extract %s: %w", archive.Name, err)
		}
	}
	return nil
}

func extractArchive(path string, dest string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".zip":
		return extractZip(path, dest)
	case ".7z":
		return extract7z(path, dest)
	default:
		return fmt.Errorf("unsupported archive type %q", filepath.Ext(path))
	}
}

func extractZip(path string, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()

	cleanDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	for _, file := range reader.File {
		targetPath := filepath.Join(cleanDest, file.Name)
		if !isPathInside(cleanDest, targetPath) {
			return fmt.Errorf("archive entry escapes destination: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, file.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		if err := copyReaderToFile(src, targetPath, file.Mode()); err != nil {
			src.Close()
			return err
		}
		if err := src.Close(); err != nil {
			return err
		}
	}
	return nil
}

func extract7z(path string, dest string) error {
	extractor, err := findExecutable("7zz", "7z", "7za")
	if err != nil {
		return errors.New("7z extraction requires 7zz, 7z, or 7za to be installed and available on PATH")
	}

	cmd := exec.Command(extractor, "x", "-y", "-o"+dest, path)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func findExecutable(names ...string) (string, error) {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
	}
	return "", errors.New("executable not found")
}

func (a *app) prepareUSB(targetRoot string, defaultApp string) error {
	xexToolPath := filepath.Join(a.extractDir, "BadUpdate Tools", "XePatcher", "XexTool.exe")
	if !fileExists(xexToolPath) {
		return fmt.Errorf("XexTool was not found at %s", xexToolPath)
	}

	fmt.Println("Copying required files to USB root...")

	if err := writeTextFile(filepath.Join(targetRoot, "name.txt"), "USB Storage Device\n"); err != nil {
		return err
	}

	info := "This drive was created with goBadBuilder.\n" +
		"Find more info here: https://github.com/Pdawg-bytes/BadBuilder\n" +
		"Configuration:\n" +
		fmt.Sprintf("-  BadUpdate target binary: %s\n", defaultApp) +
		"-  Disk formatted manually before running goBadBuilder\n"
	if err := writeTextFile(filepath.Join(targetRoot, "info.txt"), info); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(targetRoot, "Apps"), 0755); err != nil {
		return err
	}

	if err := mirrorDir(filepath.Join(a.extractDir, "BadUpdate", "Rock Band Blitz"), targetRoot); err != nil {
		return err
	}

	if err := a.installDefaultPayload(targetRoot, defaultApp); err != nil {
		return err
	}

	if err := mirrorDir(
		filepath.Join(a.extractDir, "Rock Band Blitz", filepath.FromSlash(contentFolder), "5841122D", "000D0000"),
		filepath.Join(targetRoot, filepath.FromSlash(contentFolder), "5841122D", "000D0000"),
	); err != nil {
		return err
	}

	if err := mirrorDir(
		filepath.Join(a.extractDir, "XeXmenu", filepath.FromSlash(contentFolder), "C0DE9999"),
		filepath.Join(targetRoot, filepath.FromSlash(contentFolder), "C0DE9999"),
	); err != nil {
		return err
	}

	return a.installSimpleNAND(targetRoot, xexToolPath)
}

func (a *app) installSimpleNAND(targetRoot string, xexToolPath string) error {
	sourceRoot := filepath.Join(a.extractDir, "Simple 360 NAND Flasher", "Simple 360 NAND Flasher")
	destRoot := filepath.Join(targetRoot, "Apps", "Simple 360 NAND Flasher")

	if err := mirrorDir(sourceRoot, destRoot); err != nil {
		return err
	}

	targetXex := filepath.Join(destRoot, "Default.xex")
	return a.patchXex(targetXex, xexToolPath)
}

func (a *app) installDefaultPayload(targetRoot string, defaultApp string) error {
	switch defaultApp {
	case "FreeMyXe":
		return copyFile(
			filepath.Join(a.extractDir, "FreeMyXe", "FreeMyXe.xex"),
			filepath.Join(targetRoot, "BadUpdatePayload", "default.xex"),
		)
	case "XeUnshackle":
		root := filepath.Join(a.extractDir, "XeUnshackle")
		subdirs, err := immediateDirs(root)
		if err != nil {
			return err
		}
		if len(subdirs) == 0 {
			return fmt.Errorf("no XeUnshackle payload folder found in %s", root)
		}
		return mirrorDirWithSkip(subdirs[0], targetRoot, func(path string) bool {
			return filepath.Base(path) == "README - IMPORTANT.txt"
		})
	default:
		return fmt.Errorf("unknown default app %q", defaultApp)
	}
}

func (a *app) manageHomebrew() ([]homebrewApp, error) {
	var apps []homebrewApp

	for {
		fmt.Println()
		choice, err := a.choose("Homebrew apps", []string{
			"Add Homebrew App",
			"View Added Apps",
			"Remove App",
			"Finish",
		})
		if err != nil {
			return nil, err
		}

		switch choice {
		case "Add Homebrew App":
			newApp, err := a.addHomebrewApp()
			if err != nil {
				fmt.Printf("Could not add app: %v\n", err)
				continue
			}
			apps = append(apps, newApp)
			fmt.Printf("Added %s -> %s\n", newApp.Name, filepath.Base(newApp.EntryPoint))
		case "View Added Apps":
			if len(apps) == 0 {
				fmt.Println("No homebrew apps added.")
				continue
			}
			for i, app := range apps {
				fmt.Printf("%d. %s (%s)\n", i+1, app.Name, app.EntryPoint)
			}
		case "Remove App":
			if len(apps) == 0 {
				fmt.Println("No homebrew apps to remove.")
				continue
			}
			labels := make([]string, len(apps))
			for i, app := range apps {
				labels[i] = app.Name
			}
			removeName, err := a.choose("Remove which app?", labels)
			if err != nil {
				return nil, err
			}
			for i, app := range apps {
				if app.Name == removeName {
					apps = append(apps[:i], apps[i+1:]...)
					break
				}
			}
		case "Finish":
			return apps, nil
		}
	}
}

func (a *app) addHomebrewApp() (homebrewApp, error) {
	folder, err := a.askExistingDir("Homebrew app root folder")
	if err != nil {
		return homebrewApp{}, err
	}

	xexFiles, err := rootXEXFiles(folder)
	if err != nil {
		return homebrewApp{}, err
	}

	var entryPoint string
	switch len(xexFiles) {
	case 0:
		entryPoint, err = a.askExistingFile("No .xex files found in the root. Entry point .xex path")
		if err != nil {
			return homebrewApp{}, err
		}
	case 1:
		entryPoint = xexFiles[0]
	default:
		choices := make([]string, len(xexFiles))
		for i, xex := range xexFiles {
			choices[i] = filepath.Base(xex)
		}
		selected, err := a.choose("Select entry point", choices)
		if err != nil {
			return homebrewApp{}, err
		}
		entryPoint = filepath.Join(folder, selected)
	}

	if strings.ToLower(filepath.Ext(entryPoint)) != ".xex" {
		return homebrewApp{}, fmt.Errorf("%s is not an .xex file", entryPoint)
	}
	if !isPathInside(folder, entryPoint) {
		return homebrewApp{}, errors.New("entry point must be inside the homebrew app folder")
	}

	return homebrewApp{
		Name:       filepath.Base(folder),
		Folder:     folder,
		EntryPoint: entryPoint,
	}, nil
}

func (a *app) copyHomebrew(targetRoot string, apps []homebrewApp) error {
	xexToolPath := filepath.Join(a.extractDir, "BadUpdate Tools", "XePatcher", "XexTool.exe")

	for _, app := range apps {
		destRoot := filepath.Join(targetRoot, "Apps", app.Name)
		fmt.Printf("Copying %s\n", app.Name)
		if err := mirrorDir(app.Folder, destRoot); err != nil {
			return err
		}

		relativeEntry, err := filepath.Rel(app.Folder, app.EntryPoint)
		if err != nil {
			return err
		}
		targetEntry := filepath.Join(destRoot, relativeEntry)
		if err := a.patchXex(targetEntry, xexToolPath); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) patchXex(xexPath string, xexToolPath string) error {
	if !fileExists(xexPath) {
		return fmt.Errorf("XEX file not found: %s", xexPath)
	}

	args := []string{"-m", "r", "-r", "a", xexPath}
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		cmd = exec.Command(xexToolPath, args...)
	} else if winePath, err := exec.LookPath("wine"); err == nil {
		cmd = exec.Command(winePath, append([]string{xexToolPath}, args...)...)
	} else {
		if !a.skipPatchingKnown {
			skip, err := a.askYesNo("XexTool patching requires Windows or Wine. Continue without patching copied .xex files?", false)
			if err != nil {
				return err
			}
			a.skipPatchingKnown = true
			a.skipPatching = skip
		}
		if a.skipPatching {
			fmt.Printf("Skipping patch for %s\n", xexPath)
			return nil
		}
		return fmt.Errorf("patching %s requires Windows or Wine because XexTool is a Windows executable", xexPath)
	}

	fmt.Printf("Patching %s\n", xexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("patch %s: %w\n%s", xexPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func mirrorDir(sourceDir string, destDir string) error {
	return mirrorDirWithSkip(sourceDir, destDir, nil)
}

func mirrorDirWithSkip(sourceDir string, destDir string, skip func(string) bool) error {
	info, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", sourceDir)
	}

	return filepath.WalkDir(sourceDir, func(path string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skip != nil && skip(path) {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, relativePath)

		if dirEntry.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}
		return copyFile(path, destPath)
	})
}

func copyFile(sourceFile string, destFile string) error {
	src, err := os.Open(sourceFile)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	return copyReaderToFile(src, destFile, info.Mode())
}

func copyReaderToFile(src io.Reader, destFile string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
		return err
	}

	dest, err := os.OpenFile(destFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dest, src); err != nil {
		dest.Close()
		return err
	}
	return dest.Close()
}

func writeTextFile(path string, contents string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(contents), 0644)
}

func appendInfo(targetRoot string, line string) error {
	path := filepath.Join(targetRoot, "info.txt")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(line)
	return err
}

func immediateDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func rootXEXFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".xex") {
			files = append(files, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (a *app) choose(prompt string, choices []string) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("no choices available")
	}

	for {
		fmt.Println(prompt)
		for i, choice := range choices {
			fmt.Printf("  %d. %s\n", i+1, choice)
		}
		answer, err := a.ask("Choose")
		if err != nil {
			return "", err
		}
		index, err := strconv.Atoi(strings.TrimSpace(answer))
		if err == nil && index >= 1 && index <= len(choices) {
			return choices[index-1], nil
		}
		for _, choice := range choices {
			if strings.EqualFold(strings.TrimSpace(answer), choice) {
				return choice, nil
			}
		}
		fmt.Println("Enter a number from the list.")
	}
}

func (a *app) ask(prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)
	line, err := a.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a *app) askYesNo(prompt string, defaultValue bool) (bool, error) {
	suffix := " [y/N]"
	if defaultValue {
		suffix = " [Y/n]"
	}

	for {
		answer, err := a.ask(prompt + suffix)
		if err != nil {
			return false, err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "" {
			return defaultValue, nil
		}
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Println("Please answer y or n.")
		}
	}
}

func (a *app) askExistingFile(prompt string) (string, error) {
	for {
		path, err := a.ask(prompt)
		if err != nil {
			return "", err
		}
		path = cleanInputPath(path)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return filepath.Abs(path)
		}
		fmt.Println("File does not exist.")
	}
}

func (a *app) askExistingDir(prompt string) (string, error) {
	for {
		path, err := a.ask(prompt)
		if err != nil {
			return "", err
		}
		path = cleanInputPath(path)
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return filepath.Abs(path)
		}
		fmt.Println("Directory does not exist.")
	}
}

func cleanInputPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "\"'")
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isPathInside(root string, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..")
}
