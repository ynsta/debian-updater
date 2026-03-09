package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (app *App) checkDiskMounts() {
	slog.Info("Running pre-flight safety check on disk mounts...")

	files := []string{"/etc/fstab", "/etc/crypttab"}
	hasUnsafeMounts := false

	for _, file := range files {
		content, err := os.ReadFile(file) // #nosec G304 -- only known system files are inspected here.
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
				slog.Error("Unsafe fragile device reference detected!",
					"file", file,
					"line", i+1,
					"device", device)

				hasUnsafeMounts = true
			}
		}
	}

	if hasUnsafeMounts {
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

		if !app.dryRun {
			os.Exit(1)
		}

		slog.Warn("[DRY RUN] Would have halted upgrade due to unsafe disk mounts.")

		return
	}

	slog.Info("Disk mount safety check passed. All devices use stable identifiers.")
}

func isUnsafeDevice(device string) bool {
	if !strings.HasPrefix(device, "/dev/") {
		return false
	}

	return !strings.HasPrefix(device, "/dev/mapper/") &&
		!strings.HasPrefix(device, "/dev/disk/") &&
		!strings.HasPrefix(device, "/dev/md") &&
		!strings.HasPrefix(device, "/dev/zvol/")
}

func (app *App) checkInitramfsModules() {
	slog.Info("Running pre-flight safety check on initramfs configuration...")

	file, err := os.Open("/etc/initramfs-tools/initramfs.conf")
	if err != nil {
		slog.Info("No initramfs-tools configuration found. Skipping check.")

		return
	}

	scanner := bufio.NewScanner(file)
	isDep := false

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

			break
		}
	}

	if isDep {
		app.printLines(
			"",
			"=========================================================================",
			"[FATAL ERROR] Your initramfs is configured to only pack 'dep' (dependent) modules.",
			"This means the boot image ONLY contains drivers for your CURRENT hypervisor.",
			"If you upgrade or migrate this VM, it will likely fail to boot because it",
			"will lack the necessary generic storage and network drivers.",
			"",
			"HOW TO FIX:",
			"1. Open: /etc/initramfs-tools/initramfs.conf",
			"2. Change the line 'MODULES=dep' to 'MODULES=most'",
			"3. Apply the change by running: update-initramfs -u",
			"=========================================================================",
			"",
		)

		if !app.dryRun {
			closeWithWarning("/etc/initramfs-tools/initramfs.conf", file)
			os.Exit(1)
		}

		slog.Warn("[DRY RUN] Would have halted upgrade due to unsafe initramfs MODULES=dep configuration.")
		closeWithWarning("/etc/initramfs-tools/initramfs.conf", file)

		return
	}

	closeWithWarning("/etc/initramfs-tools/initramfs.conf", file)
	slog.Info("Initramfs configuration is safe (MODULES=most).")
}

func (app *App) checkWeakGPGKeys() {
	slog.Info("Scanning for weak SHA-1 signatures in APT keyrings...")

	var keyFiles []string

	dirs := []string{"/etc/apt/keyrings", "/usr/share/keyrings"}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
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
		cmd := exec.CommandContext(context.Background(), "gpg", "--no-default-keyring", "--keyring", keyFile, "--list-packets")

		out, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(out), "digest algo 2") {
			slog.Warn("Weak SHA-1 signature found in keyring", "file", keyFile)

			foundWeak = true
		}
	}

	if foundWeak {
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

	slog.Info("GPG keyring scan passed. No weak SHA-1 signatures detected.")
}
