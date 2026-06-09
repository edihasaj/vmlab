package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type linuxSetupOptions struct {
	vm              string
	user            string
	host            string
	identity        string
	prefix          string
	share           []string
	crabboxWorkRoot string
	repoDir         string
	prlctl          string
	noSSHConfig     bool
	noInstall       bool
	noRepoConfig    bool
	force           bool
}

func instanceSetupLinuxCmd() *cobra.Command {
	o := linuxSetupOptions{
		user:            "parallels",
		crabboxWorkRoot: "/work/crabbox",
		repoDir:         ".",
	}
	c := &cobra.Command{
		Use:   "setup-linux --vm <parallels-vm-name>",
		Short: "Bootstrap a Parallels Linux VM for SSH, crabbox, and vmlab targets",
		Example: `  vmlab instance setup-linux --vm "Ubuntu 24.04.3 ARM64" --host 10.211.55.7 \
    --prefix ubuntu --share farm=$HOME/Projects/farm`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.vm == "" {
				return fmt.Errorf("--vm is required")
			}
			if o.prefix == "" {
				o.prefix = setupName(o.vm)
			}
			if o.identity == "" {
				o.identity = filepath.Join(mustHomeDir(), ".ssh", "vmlab_"+o.prefix)
			}
			return runLinuxSetup(cmd.Context(), cmd.OutOrStdout(), o)
		},
	}
	c.Flags().StringVar(&o.vm, "vm", "", "Parallels VM name (required)")
	c.Flags().StringVar(&o.user, "user", o.user, "Linux guest user")
	c.Flags().StringVar(&o.host, "host", "", "guest SSH host/IP; detected with prlctl exec hostname -I when empty")
	c.Flags().StringVar(&o.identity, "identity", "", "SSH identity path; created when missing")
	c.Flags().StringVar(&o.prefix, "prefix", "", "target/flow name prefix (default: sanitized VM name)")
	c.Flags().StringArrayVar(&o.share, "share", nil, "Parallels shared folder name=hostPath, repeatable")
	c.Flags().StringVar(&o.crabboxWorkRoot, "crabbox-work-root", o.crabboxWorkRoot, "writable crabbox work root in the guest")
	c.Flags().StringVar(&o.repoDir, "repo-dir", o.repoDir, "repo dir for vmlab/targets and vmlab/flows")
	c.Flags().StringVar(&o.prlctl, "prlctl", "", "prlctl binary path (default: PATH or Parallels app bundle)")
	c.Flags().BoolVar(&o.noSSHConfig, "no-ssh-config", false, "do not append ~/.ssh/config")
	c.Flags().BoolVar(&o.noInstall, "no-install", false, "skip apt/systemctl guest bootstrap")
	c.Flags().BoolVar(&o.noRepoConfig, "no-repo-config", false, "do not write repo-local vmlab target/flow files")
	c.Flags().BoolVar(&o.force, "force", false, "overwrite generated repo config files")
	return c
}

func runLinuxSetup(ctx context.Context, out io.Writer, o linuxSetupOptions) error {
	identity := config.ExpandPath(o.identity)
	if err := ensureSSHKey(ctx, identity, o.prefix); err != nil {
		return err
	}
	pub, err := os.ReadFile(identity + ".pub")
	if err != nil {
		return fmt.Errorf("read public key: %w", err)
	}
	prlctl, err := resolvePrlctl(o.prlctl)
	if err != nil {
		return err
	}
	if err := addParallelsShares(ctx, prlctl, o.vm, o.share); err != nil {
		return err
	}
	if !o.noInstall {
		if err := bootstrapLinuxGuest(ctx, prlctl, o.vm, o.user, string(pub), o.crabboxWorkRoot); err != nil {
			return err
		}
	}
	host := o.host
	if host == "" {
		host, err = detectGuestHost(ctx, prlctl, o.vm)
		if err != nil {
			return err
		}
	}
	if !o.noSSHConfig {
		if err := ensureSSHConfig(o.prefix, host, o.user, identity); err != nil {
			return err
		}
	}
	if !o.noRepoConfig {
		files, err := writeLinuxRepoConfig(o.repoDir, o.prefix, host, o.user, identity, o.vm, o.crabboxWorkRoot, o.force)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Fprintf(out, "wrote %s\n", f)
		}
	}
	fmt.Fprintf(out, "ready: %s\n", o.prefix)
	fmt.Fprintf(out, "smoke ssh:       vmlab run %s-ssh vmlab/flows/%s-smoke.yaml --json\n", o.prefix, o.prefix)
	fmt.Fprintf(out, "smoke crabbox:   vmlab run %s-crabbox vmlab/flows/%s-smoke.yaml --json\n", o.prefix, o.prefix)
	fmt.Fprintf(out, "smoke parallels: vmlab run %s-parallels vmlab/flows/%s-parallels-smoke.yaml --json\n", o.prefix, o.prefix)
	return nil
}

func ensureSSHKey(ctx context.Context, path, prefix string) error {
	if _, err := os.Stat(path); err == nil {
		if _, err := os.Stat(path + ".pub"); err == nil {
			return nil
		}
		return fmt.Errorf("identity exists but public key is missing: %s.pub", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", path, "-C", "vmlab-"+prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh-keygen: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolvePrlctl(flag string) (string, error) {
	if flag != "" {
		return config.ExpandPath(flag), nil
	}
	if p, err := exec.LookPath("prlctl"); err == nil {
		return p, nil
	}
	cand := "/Applications/Parallels Desktop.app/Contents/MacOS/prlctl"
	if _, err := os.Stat(cand); err == nil {
		return cand, nil
	}
	return "", fmt.Errorf("prlctl not found; pass --prlctl")
}

func addParallelsShares(ctx context.Context, prlctl, vm string, shares []string) error {
	for _, raw := range shares {
		name, hostPath, ok := strings.Cut(raw, "=")
		if !ok || name == "" || hostPath == "" {
			return fmt.Errorf("invalid --share %q (expected name=hostPath)", raw)
		}
		hostPath = config.ExpandPath(hostPath)
		args := []string{"set", vm, "--shf-host-add", name, "--path", hostPath, "--mode", "rw"}
		out, err := runCombined(ctx, prlctl, args...)
		if err != nil && !strings.Contains(strings.ToLower(out), "already") {
			return fmt.Errorf("add share %s: %w: %s", name, err, strings.TrimSpace(out))
		}
	}
	return nil
}

func bootstrapLinuxGuest(ctx context.Context, prlctl, vm, user, pubKey, workRoot string) error {
	tmp, err := os.MkdirTemp("", "vmlab-linux-setup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	scriptPath := filepath.Join(tmp, "bootstrap.sh")
	script := linuxBootstrapScript(user, pubKey, workRoot)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}
	if out, err := runCombined(ctx, prlctl, "set", vm, "--shf-host-add", "vmlab-setup", "--path", tmp, "--mode", "rw"); err != nil && !strings.Contains(strings.ToLower(out), "already") {
		return fmt.Errorf("add setup share: %w: %s", err, strings.TrimSpace(out))
	}
	if out, err := runCombined(ctx, prlctl, "exec", vm, "/bin/bash", "/media/psf/vmlab-setup/bootstrap.sh"); err != nil {
		return fmt.Errorf("guest bootstrap: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func linuxBootstrapScript(user, pubKey, workRoot string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
if command -v apt-get >/dev/null 2>&1; then
  apt-get update
  apt-get install -y openssh-server avahi-daemon curl git ca-certificates build-essential jq
fi
home_dir=$(getent passwd %[1]q | cut -d: -f6)
group_name=$(id -gn %[1]q)
install -d -m 700 -o %[1]q -g "$group_name" "$home_dir/.ssh"
touch "$home_dir/.ssh/authorized_keys"
grep -qxF %[2]q "$home_dir/.ssh/authorized_keys" || echo %[2]q >> "$home_dir/.ssh/authorized_keys"
chown %[1]q:"$group_name" "$home_dir/.ssh/authorized_keys"
chmod 600 "$home_dir/.ssh/authorized_keys"
systemctl enable --now ssh >/dev/null 2>&1 || systemctl enable --now sshd >/dev/null 2>&1 || true
systemctl enable --now avahi-daemon >/dev/null 2>&1 || true
install -d -m 775 -o %[1]q -g "$group_name" %[3]q
`, user, strings.TrimSpace(pubKey), workRoot)
}

func detectGuestHost(ctx context.Context, prlctl, vm string) (string, error) {
	out, err := runCombined(ctx, prlctl, "exec", vm, "hostname", "-I")
	if err != nil {
		return "", fmt.Errorf("detect guest host: %w: %s", err, strings.TrimSpace(out))
	}
	for _, field := range strings.Fields(out) {
		ip := net.ParseIP(field)
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			return field, nil
		}
	}
	return "", fmt.Errorf("detect guest host: no IPv4 in %q", strings.TrimSpace(out))
}

func ensureSSHConfig(alias, host, user, identity string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.Contains(existing, []byte("Host "+alias+"\n")) {
		return nil
	}
	if len(existing) > 0 {
		backup := fmt.Sprintf("%s.backup-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			return fmt.Errorf("backup ssh config: %w", err)
		}
	}
	var b strings.Builder
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\nHost " + alias + "\n")
	b.WriteString("  HostName " + host + "\n")
	b.WriteString("  User " + user + "\n")
	b.WriteString("  IdentityFile " + identity + "\n")
	b.WriteString("  IdentitiesOnly yes\n")
	b.WriteString("  StrictHostKeyChecking accept-new\n")
	return os.WriteFile(path, append(existing, []byte(b.String())...), 0o600)
}

func writeLinuxRepoConfig(repoDir, prefix, host, user, identity, vm, workRoot string, force bool) ([]string, error) {
	base := filepath.Join(config.ExpandPath(repoDir), "vmlab")
	targetDir := filepath.Join(base, "targets")
	flowDir := filepath.Join(base, "flows")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(flowDir, 0o755); err != nil {
		return nil, err
	}
	files := map[string]any{
		filepath.Join(targetDir, prefix+"-ssh.yaml"): targetDoc(prefix+"-ssh", "ssh", []string{"linux", "parallels", "vm"}, map[string]any{
			"ssh": map[string]any{"host": host, "user": user, "identity": identity, "strictHost": "accept-new"},
			"os":  "linux",
		}),
		filepath.Join(targetDir, prefix+"-crabbox.yaml"): targetDoc(prefix+"-crabbox", "crabbox", []string{"linux", "parallels", "vm", "crabbox"}, map[string]any{
			"crabbox": map[string]any{"staticHost": prefix, "staticUser": user},
			"os":      "linux",
		}),
		filepath.Join(targetDir, prefix+"-parallels.yaml"): targetDoc(prefix+"-parallels", "parallels-guest", []string{"linux", "parallels", "vm"}, map[string]any{
			"parallels": map[string]any{"vm": vm},
			"os":        "linux",
		}),
		filepath.Join(flowDir, prefix+"-smoke.yaml"): map[string]any{
			"name": prefix + "-smoke",
			"steps": []map[string]any{
				{"run": "uname -a"},
				{"run": "id"},
				{"assert": "test -w " + shellQuote(workRoot)},
			},
		},
		filepath.Join(flowDir, prefix+"-parallels-smoke.yaml"): map[string]any{
			"name": prefix + "-parallels-smoke",
			"steps": []map[string]any{
				{"run": "uname -a"},
				{"assert": "test -d /media/psf || test -d /mnt/psf"},
			},
		},
	}
	var wrote []string
	for path, doc := range files {
		if _, err := os.Stat(path); err == nil && !force {
			return nil, fmt.Errorf("%s exists; pass --force to overwrite", path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		data, err := yaml.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		wrote = append(wrote, path)
	}
	return wrote, nil
}

func targetDoc(name, transport string, tags []string, settings map[string]any) map[string]any {
	doc := map[string]any{
		"name":      name,
		"transport": transport,
		"tags":      tags,
	}
	for k, v := range settings {
		doc[k] = v
	}
	return doc
}

func setupName(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "linux-vm"
	}
	return s
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func runCombined(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
