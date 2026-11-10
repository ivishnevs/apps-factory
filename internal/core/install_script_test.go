package core

import (
	"os/exec"
	"strings"
	"testing"
)

// TestInstallScriptLinuxReadiness (node-readiness 3.1, 3.2): install.sh parses, refuses root without --os-user
// before it installs anything, enables linger, checks the git identity and the sandbox, and only prints the commands
// that change the host's packages or security settings (never runs them).
func TestInstallScriptLinuxReadiness(t *testing.T) {
	if sh, err := exec.LookPath("sh"); err == nil {
		if out, err := exec.Command(sh, "-n", "-c", installScript).CombinedOutput(); err != nil {
			t.Fatalf("install.sh does not parse: %v %s", err, out)
		}
	}
	root := strings.Index(installScript, `[ "$(id -u)" = 0 ]`)
	reexec := strings.Index(installScript, `exec sudo -u "$OS_USER"`)
	download := strings.Index(installScript, "Downloading the node")
	if root < 0 || root > reexec || root > download || !strings.Contains(installScript, "--os-user factory") {
		t.Fatal("no root refusal before the install")
	}
	for _, want := range []string{"loginctl enable-linger", "git config --global user.email", "bwrap --unshare-user", "socat", "apparmor_restrict_unprivileged_userns", "/etc/apparmor.d/bwrap", "node --version"} {
		if !strings.Contains(installScript, want) {
			t.Errorf("install.sh lacks %q", want)
		}
	}
	for i, line := range strings.Split(installScript, "\n") {
		l := strings.TrimSpace(line)
		for _, change := range []string{"apparmor_parser", "apt-get install", "dnf install", "sysctl kernel.", "sysctl -w", "tee /etc/"} {
			if strings.Contains(l, change) && !strings.HasPrefix(l, "say ") && !strings.HasPrefix(l, "#") {
				t.Errorf("line %d changes the host instead of printing the command: %s", i+1, l)
			}
		}
	}
}
