package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const githubReleaseAPI = "https://api.github.com/repos/Veverke/ErrorProbe/releases/latest"

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the errorprobe binary to the latest release",
	Long: `Upgrade downloads the latest errorprobe release from GitHub, verifies the
SHA-256 checksum, and atomically replaces the running binary.

On Linux/macOS an atomic rename is used. If the install directory is not
writable, the command exits with a clear error suggesting sudo.

On Windows the current binary is renamed to errorprobe.exe.old and the .old
file is silently cleaned up on the next run.`,
	Args: cobra.NoArgs,
	RunE: upgradeRun,
}

type upgradeReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type upgradeReleaseResponse struct {
	TagName string                `json:"tag_name"`
	Assets  []upgradeReleaseAsset `json:"assets"`
}

func upgradeRun(_ *cobra.Command, _ []string) error {
	// Fetch latest release info from GitHub.
	rel, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("fetching release info: %w", err)
	}

	// Compare versions: strip leading "v" from tag for comparison.
	latest := strings.TrimPrefix(rel.TagName, "v")
	current := Version
	if latest == current || rel.TagName == current {
		fmt.Printf("errorprobe is already at the latest version (%s)\n", current)
		return nil
	}

	// Locate the platform-specific binary asset and checksums.txt.
	assetName := platformAssetName()
	var binaryURL, checksumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			binaryURL = a.BrowserDownloadURL
		case "checksums.txt":
			checksumsURL = a.BrowserDownloadURL
		}
	}
	if binaryURL == "" {
		return fmt.Errorf("no asset '%s' found in release %s", assetName, rel.TagName)
	}
	if checksumsURL == "" {
		return fmt.Errorf("checksums.txt not found in release %s", rel.TagName)
	}

	fmt.Printf("Upgrading errorprobe from %s to %s...\n", current, latest)

	// Download and parse checksums.txt first (small file).
	checksumsData, err := upgradeHTTPGetBytes(checksumsURL)
	if err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}
	expectedHash, err := upgradeParseChecksum(checksumsData, assetName)
	if err != nil {
		return err
	}

	// Resolve current executable path — determines install dir.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	execPath = filepath.Clean(execPath)

	// Download new binary to a temp file in the same directory as the running
	// binary. Same filesystem guarantees an atomic rename on all platforms.
	tmpPath := execPath + ".new"
	if err := upgradeDownloadFile(binaryURL, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("downloading binary: %w", err)
	}

	// Verify checksum before touching the installed binary.
	if err := upgradeVerifySHA256(tmpPath, expectedHash); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	fmt.Println("Checksum verified.")

	// Ensure the new binary is executable on Unix.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("setting executable permission: %w", err)
		}
	}

	// Replace the binary atomically (platform-specific).
	if err := replaceExecutable(execPath, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	fmt.Printf("errorprobe upgraded to %s. Run 'errorprobe --version' to confirm.\n", latest)
	return nil
}

// cleanupUpgradeArtifacts removes the .old backup left by a Windows in-place
// upgrade on the previous run. On non-Windows systems this is a no-op.
func cleanupUpgradeArtifacts() {
	if runtime.GOOS != "windows" {
		return
	}
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Clean(execPath) + ".old") // silent; may not exist
}

// ── helpers ───────────────────────────────────────────────────────────────────

func fetchLatestRelease() (*upgradeReleaseResponse, error) {
	body, err := upgradeHTTPGetBytes(githubReleaseAPI)
	if err != nil {
		return nil, err
	}
	var rel upgradeReleaseResponse
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parsing release JSON: %w", err)
	}
	return &rel, nil
}

func upgradeHTTPGetBytes(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "errorprobe-upgrade/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func upgradeDownloadFile(url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "errorprobe-upgrade/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading binary", resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func upgradeParseChecksum(data []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("hash for '%s' not found in checksums.txt", filename)
}

func upgradeVerifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum mismatch:\n  expected: %s\n  got:      %s", expected, actual)
	}
	return nil
}

func platformAssetName() string {
	name := fmt.Sprintf("errorprobe-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
