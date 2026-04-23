package main

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (app *App) checkDiskMounts() {
	slog.Info("Running pre-flight safety check on disk mounts...", "step", "preflight.disk_mounts")

	files := []string{"/etc/fstab", "/etc/crypttab"}
	hasUnsafeMounts := false

	for _, file := range files {
		content, err := app.fs.ReadFile(file)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}

			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}

			device := fields[0]
			if isUnsafeDevice(device) {
				slog.Error("Unsafe fragile device reference detected",
					"step", "preflight.disk_mounts",
					"file", file,
					"line", i+1,
					"device", device)

				hasUnsafeMounts = true
			}
		}
	}

	if hasUnsafeMounts {
		slog.Error("Preflight failed: unsafe disk mount references", "step", "preflight.disk_mounts", "remediation", "replace /dev/sdX with UUID= in /etc/fstab")
		app.printLines(
			"",
			"=========================================================================",
			"[FATAL ERROR] Your system uses fragile '/dev/...' names for disk mounts.",
			"During a major kernel upgrade, disk ordering can change (e.g., /dev/sda ",
			"becomes /dev/sdb). This will cause your system to fail to boot.",
			"",
			"HOW TO FIX:",
			"1. Run the command: blkid",
			"2. Find the UUID for the failing devices listed above.",
			"3. Edit /etc/fstab and replace '/dev/sdX' with 'UUID=your-uuid-here'",
			"=========================================================================",
			"",
		)

		if !app.cfg.DryRun {
			os.Exit(1)
		}

		slog.Warn("Would have halted upgrade due to unsafe disk mounts", "step", "preflight.disk_mounts", "dry_run", true)

		return
	}

	slog.Info("Disk mount safety check passed. All devices use stable identifiers.", "step", "preflight.disk_mounts")
}

func isUnsafeDevice(device string) bool {
	if !strings.HasPrefix(device, "/dev/") {
		return false
	}

	// Optical drives and floppies are generally not boot-critical for the upgrade
	// and often don't have stable identifiers like UUIDs.
	if strings.HasPrefix(device, "/dev/sr") ||
		strings.HasPrefix(device, "/dev/cdrom") ||
		strings.HasPrefix(device, "/dev/fd") {
		return false
	}

	return !strings.HasPrefix(device, "/dev/mapper/") &&
		!strings.HasPrefix(device, "/dev/disk/") &&
		!strings.HasPrefix(device, "/dev/md") &&
		!strings.HasPrefix(device, "/dev/zvol/")
}

func (app *App) checkInitramfsModules() {
	slog.Info("Running pre-flight safety check on initramfs configuration...", "step", "preflight.initramfs")

	matches, _ := app.fs.Glob("/etc/initramfs-tools/conf.d/*")
	files := make([]string, 0, 1+len(matches))
	files = append(files, "/etc/initramfs-tools/initramfs.conf")
	files = append(files, matches...)

	isDep := false

	var failingFile string

	for _, path := range files {
		data, err := app.fs.ReadFile(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "#") {
				continue
			}

			val, ok := strings.CutPrefix(line, "MODULES=")
			if !ok {
				continue
			}

			val = strings.TrimSpace(val)
			if val == "dep" {
				isDep = true
				failingFile = path

				break
			}
		}

		if isDep {
			break
		}
	}

	if isDep {
		slog.Error("Preflight failed: initramfs MODULES=dep restricts drivers to current hardware", "step", "preflight.initramfs", "file", failingFile, "remediation", "set MODULES=most and run update-initramfs -u")
		app.printLines(
			"",
			"=========================================================================",
			"[FATAL ERROR] Your initramfs is configured to only pack 'dep' (dependent) modules.",
			"Detected in: "+failingFile,
			"This means the boot image ONLY contains drivers for your CURRENT hypervisor.",
			"If you upgrade or migrate this VM, it will likely fail to boot because it",
			"will lack the necessary generic storage and network drivers.",
			"",
			"HOW TO FIX:",
			"1. Open: "+failingFile,
			"2. Change the line 'MODULES=dep' to 'MODULES=most'",
			"3. Apply the change by running: update-initramfs -u",
			"=========================================================================",
			"",
		)

		if !app.cfg.DryRun {
			os.Exit(1)
		}

		slog.Warn("Would have halted upgrade due to unsafe initramfs MODULES=dep configuration", "step", "preflight.initramfs", "file", failingFile, "dry_run", true)

		return
	}

	slog.Info("Initramfs configuration is safe (no MODULES=dep found).", "step", "preflight.initramfs")
}

func (app *App) checkWeakGPGKeys(ctx context.Context) {
	slog.Info("Scanning for weak SHA-1 signatures in APT keyrings...", "step", "preflight.gpg")

	var keyFiles []string

	dirs := []string{"/etc/apt/keyrings", "/usr/share/keyrings"}

	for _, dir := range dirs {
		entries, err := app.fs.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				keyFiles = append(keyFiles, filepath.Join(dir, entry.Name()))
			}
		}
	}

	foundWeak := false

	for _, keyFile := range keyFiles {
		// gpg --list-packets outputs algorithm IDs. ID 2 is SHA-1.
		// #nosec G204 -- the command is fixed and key files come from trusted system directories.
		cmd := exec.CommandContext(ctx, "gpg", "--no-default-keyring", "--keyring", keyFile, "--list-packets")

		out, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(out), "digest algo 2") {
			slog.Warn("Weak SHA-1 signature found in keyring", "step", "preflight.gpg", "file", keyFile)

			foundWeak = true
		}
	}

	if foundWeak {
		slog.Warn("Preflight warning: weak SHA-1 GPG signatures in APT keyrings", "step", "preflight.gpg", "remediation", "reinstall affected third-party repositories per vendor instructions")
		app.printLines(
			"",
			"=========================================================================",
			"[WARNING] Weak SHA-1 GPG keys detected in your APT keyrings!",
			"Debian 13 (Trixie) and newer strictly reject SHA-1 signatures.",
			"Third-party repositories using these keys WILL fail to update.",
			"",
			"OFFICIAL RECOMMENDATION:",
			"Do not attempt to manually hack or bypass this. Please visit the official",
			"documentation for the affected software (Docker, Node, Cloudflare, etc.)",
			"and completely reinstall their repository per their latest instructions.",
			"=========================================================================",
			"",
		)

		return
	}

	slog.Info("GPG keyring scan passed. No weak SHA-1 signatures detected.", "step", "preflight.gpg")
}
