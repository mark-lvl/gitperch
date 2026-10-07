package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease writes archives for platforms in the GitHub releases layout under
// root. Each archived "binary" is a shell script that reports its platform.
func fakeRelease(t *testing.T, root, version string, latest bool, platforms ...string) {
	t.Helper()
	dir := filepath.Join(root, "download", "v"+version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sums strings.Builder
	for _, platform := range platforms {
		name := "gitperch_" + version + "_" + strings.Replace(platform, "/", "_", 1)
		script := fmt.Sprintf("#!/bin/sh\necho 'gitperch %s %s'\n", version, platform)
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: name + "/gitperch", Mode: 0o755, Size: int64(len(script))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(script)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, name+".tar.gz"), buf.String(), 0o644)
		fmt.Fprintf(&sums, "%x  %s.tar.gz\n", sha256.Sum256(buf.Bytes()), name)
	}
	sums.WriteString(fmt.Sprintf("%x  install.sh\n", sha256.Sum256([]byte("installer"))))
	writeFile(t, filepath.Join(dir, "checksums.txt"), sums.String(), 0o644)
	if latest {
		writeFile(t, filepath.Join(root, "latest", "download", "checksums.txt"), sums.String(), 0o644)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

type installer struct {
	server     *httptest.Server
	releases   string
	installDir string
	fakeBin    string
	env        []string
}

func newInstaller(t *testing.T) *installer {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the installer targets Unix shells")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		if _, err := exec.LookPath("wget"); err != nil {
			t.Skip("needs curl or wget")
		}
	}
	tmp := t.TempDir()
	in := &installer{
		releases:   filepath.Join(tmp, "releases"),
		installDir: filepath.Join(tmp, "home", ".local", "bin"),
		fakeBin:    filepath.Join(tmp, "fakebin"),
	}
	fakeRelease(t, in.releases, "0.3.0", false, "linux/amd64", "linux/arm64")
	fakeRelease(t, in.releases, "0.4.0", true, "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")
	in.server = httptest.NewServer(http.FileServer(http.Dir(in.releases)))
	t.Cleanup(in.server.Close)
	in.platform(t, "Linux", "x86_64", "")
	in.env = []string{
		"PATH=" + in.fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + filepath.Join(tmp, "home"),
		"TMPDIR=" + tmp,
		"SHELL=/bin/zsh",
		"GITPERCH_DOWNLOAD_URL=" + in.server.URL,
		"NO_PROXY=127.0.0.1,localhost",
		"no_proxy=127.0.0.1,localhost",
	}
	return in
}

// platform installs fake uname and sysctl commands; an empty arm64 leaves
// hw.optional.arm64 undefined, as on Intel Macs.
func (in *installer) platform(t *testing.T, kernel, machine, arm64 string) {
	t.Helper()
	writeFile(t, filepath.Join(in.fakeBin, "uname"), fmt.Sprintf(
		"#!/bin/sh\ncase $1 in -s) echo '%s' ;; -m) echo '%s' ;; *) exit 1 ;; esac\n", kernel, machine), 0o755)
	sysctl := "#!/bin/sh\nexit 1\n"
	if arm64 != "" {
		sysctl = "#!/bin/sh\necho " + arm64 + "\n"
	}
	writeFile(t, filepath.Join(in.fakeBin, "sysctl"), sysctl, 0o755)
}

func (in *installer) run(t *testing.T, extraEnv ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append(append([]string{}, in.env...), extraEnv...)
	cmd.Env = append(cmd.Env, "INSTALL_DIR="+in.installDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (in *installer) installed(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(in.installDir, "gitperch")).Output()
	if err != nil {
		t.Fatalf("run installed gitperch: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestInstallPicksTheLatestArchiveForThePlatform(t *testing.T) {
	for _, tc := range []struct {
		kernel, machine, arm64, want string
	}{
		{"Linux", "x86_64", "", "linux/amd64"},
		{"Linux", "aarch64", "", "linux/arm64"},
		{"Darwin", "arm64", "1", "darwin/arm64"},
		{"Darwin", "x86_64", "", "darwin/amd64"},
		// A shell under Rosetta on Apple Silicon.
		{"Darwin", "x86_64", "1", "darwin/arm64"},
	} {
		t.Run(tc.kernel+"/"+tc.machine+"/"+tc.arm64, func(t *testing.T) {
			in := newInstaller(t)
			in.platform(t, tc.kernel, tc.machine, tc.arm64)
			out, err := in.run(t)
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if got, want := in.installed(t), "gitperch 0.4.0 "+tc.want; got != want {
				t.Fatalf("installed %q, want %q\n%s", got, want, out)
			}
			if !strings.Contains(out, "Installed gitperch 0.4.0 "+tc.want) {
				t.Fatalf("output does not report the install:\n%s", out)
			}
			// INSTALL_DIR is not on the test PATH.
			if !strings.Contains(out, "Add "+in.installDir+" to PATH") || !strings.Contains(out, "~/.zshrc") {
				t.Fatalf("output does not explain PATH:\n%s", out)
			}
		})
	}
}

func TestInstallPinnedVersionAndUpgrade(t *testing.T) {
	in := newInstaller(t)
	if out, err := in.run(t, "GITPERCH_VERSION=v0.3.0"); err != nil {
		t.Fatalf("install 0.3.0: %v\n%s", err, out)
	}
	if got := in.installed(t); got != "gitperch 0.3.0 linux/amd64" {
		t.Fatalf("installed %q", got)
	}
	if out, err := in.run(t); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if got := in.installed(t); got != "gitperch 0.4.0 linux/amd64" {
		t.Fatalf("after upgrade %q", got)
	}
	entries, err := os.ReadDir(in.installDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("install dir holds %v, %v; want only gitperch", entries, err)
	}
}

func TestInstallRefusals(t *testing.T) {
	for _, tc := range []struct {
		name             string
		kernel, machine  string
		env              []string
		corrupt, symlink bool
		want             string
	}{
		{name: "windows shell", kernel: "MINGW64_NT-10.0", machine: "x86_64", want: "run this installer inside WSL"},
		{name: "unsupported arch", kernel: "Linux", machine: "armv7l", want: "go install github.com/mark-lvl/gitperch/cmd/gitperch@latest"},
		{name: "release without platform", kernel: "Darwin", machine: "arm64", env: []string{"GITPERCH_VERSION=0.3.0"}, want: "release v0.3.0 has no darwin/arm64 archive"},
		{name: "missing release", kernel: "Linux", machine: "x86_64", env: []string{"GITPERCH_VERSION=9.9.9"}, want: "could not download"},
		{name: "bad version", kernel: "Linux", machine: "x86_64", env: []string{"GITPERCH_VERSION=0.4.0/../x"}, want: "is not a version"},
		{name: "checksum mismatch", kernel: "Linux", machine: "x86_64", corrupt: true, want: "checksum mismatch"},
		{name: "symlink target", kernel: "Linux", machine: "x86_64", symlink: true, want: "is a symlink or not a regular file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newInstaller(t)
			in.platform(t, tc.kernel, tc.machine, "")
			if tc.corrupt {
				archive := filepath.Join(in.releases, "download", "v0.4.0", "gitperch_0.4.0_linux_amd64.tar.gz")
				writeFile(t, archive, "tampered", 0o644)
			}
			link := filepath.Join(in.installDir, "gitperch")
			if tc.symlink {
				if err := os.MkdirAll(in.installDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/nonexistent/scripts/gitperch-dev", link); err != nil {
					t.Fatal(err)
				}
			}
			out, err := in.run(t, tc.env...)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("err %v, output does not contain %q:\n%s", err, tc.want, out)
			}
			if tc.symlink {
				if target, err := os.Readlink(link); err != nil || target != "/nonexistent/scripts/gitperch-dev" {
					t.Fatalf("symlink changed: %q, %v", target, err)
				}
			} else if _, err := os.Lstat(link); !os.IsNotExist(err) {
				t.Fatalf("refused install left %s behind: %v", link, err)
			}
		})
	}
}
