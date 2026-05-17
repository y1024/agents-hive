package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
)

func TestEnsureFileConvDependenciesUsesExistingMinerU(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script is unix-specific")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mineru")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake mineru: %v", err)
	}
	cfg := config.Default()
	cfg.FileConv.Markdown.PDF.Command.Binary = bin
	disabled := false
	cfg.FileConv.Markdown.PDF.Install.Enabled = &disabled

	if err := ensureFileConvDependencies(context.Background(), cfg, zap.NewNop()); err != nil {
		t.Fatalf("ensureFileConvDependencies() error = %v", err)
	}
	if cfg.FileConv.Markdown.PDF.Command.Binary != bin {
		t.Fatalf("binary = %q, want %q", cfg.FileConv.Markdown.PDF.Command.Binary, bin)
	}
}

func TestEnsureFileConvDependenciesInstallsMinerUWhenMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script is unix-specific")
	}
	root := t.TempDir()
	installDir := filepath.Join(root, "install")
	installer := filepath.Join(root, "installer.sh")
	script := "#!/bin/sh\nmkdir -p \"$1/bin\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"$1/bin/mineru\"\nchmod +x \"$1/bin/mineru\"\n"
	if err := os.WriteFile(installer, []byte(script), 0o755); err != nil {
		t.Fatalf("write installer: %v", err)
	}
	cfg := config.Default()
	cfg.FileConv.Markdown.PDF.Command.Binary = "mineru"
	cfg.FileConv.Markdown.PDF.Install.InstallDir = installDir
	enabled := true
	cfg.FileConv.Markdown.PDF.Install.Enabled = &enabled
	cfg.FileConv.Markdown.PDF.Install.Timeout = 10 * time.Second
	cfg.FileConv.Markdown.PDF.Install.Command = config.CommandSpec{
		Binary: installer,
		Args:   []string{"{install_dir}"},
	}

	if err := ensureFileConvDependencies(context.Background(), cfg, zap.NewNop()); err != nil {
		t.Fatalf("ensureFileConvDependencies() error = %v", err)
	}
	want := filepath.Join(installDir, "bin", "mineru")
	if cfg.FileConv.Markdown.PDF.Command.Binary != want {
		t.Fatalf("binary = %q, want %q", cfg.FileConv.Markdown.PDF.Command.Binary, want)
	}
}

func TestEnsureFileConvDependenciesInstallsMinerUWithBuiltinVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script is unix-specific")
	}
	root := t.TempDir()
	installDir := filepath.Join(root, "install")
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	python := filepath.Join(fakeBin, "python3")
	script := `#!/bin/sh
set -eu
if [ "$1" != "-m" ] || [ "$2" != "venv" ]; then
  exit 2
fi
dir="$3"
mkdir -p "$dir/bin"
cat > "$dir/bin/pip" <<'PIP'
#!/bin/sh
set -eu
bindir="${0%/*}"
cat > "$bindir/mineru" <<'MINERU'
#!/bin/sh
exit 0
MINERU
chmod +x "$bindir/mineru"
PIP
chmod +x "$dir/bin/pip"
`
	if err := os.WriteFile(python, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")

	cfg := config.Default()
	cfg.FileConv.Markdown.PDF.Command.Binary = "mineru"
	cfg.FileConv.Markdown.PDF.Install.InstallDir = installDir
	enabled := true
	cfg.FileConv.Markdown.PDF.Install.Enabled = &enabled
	cfg.FileConv.Markdown.PDF.Install.Timeout = 10 * time.Second
	cfg.FileConv.Markdown.PDF.Install.Command = config.CommandSpec{
		Binary: "builtin:python-venv-pip",
		Args:   []string{"mineru[all]"},
	}

	if err := ensureFileConvDependencies(context.Background(), cfg, zap.NewNop()); err != nil {
		t.Fatalf("ensureFileConvDependencies() error = %v", err)
	}
	want := filepath.Join(installDir, "bin", "mineru")
	if cfg.FileConv.Markdown.PDF.Command.Binary != want {
		t.Fatalf("binary = %q, want %q", cfg.FileConv.Markdown.PDF.Command.Binary, want)
	}
}

func TestEnsureFileConvDependenciesFailsWhenMinerUMissingAndInstallDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.FileConv.Markdown.PDF.Command.Binary = filepath.Join(t.TempDir(), "missing-mineru")
	disabled := false
	cfg.FileConv.Markdown.PDF.Install.Enabled = &disabled

	err := ensureFileConvDependencies(context.Background(), cfg, zap.NewNop())
	if err == nil {
		t.Fatal("expected missing MinerU error")
	}
}

func TestEnsureFileConvDependenciesSkipsNonMinerUProvider(t *testing.T) {
	cfg := config.Default()
	cfg.FileConv.Markdown.PDF.Provider = "external"
	cfg.FileConv.Markdown.PDF.Command.Binary = filepath.Join(t.TempDir(), "missing")

	if err := ensureFileConvDependencies(context.Background(), cfg, zap.NewNop()); err != nil {
		t.Fatalf("ensureFileConvDependencies() error = %v", err)
	}
}
