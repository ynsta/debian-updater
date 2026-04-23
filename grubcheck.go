package main

import (
	"context"
	"log/slog"
	"strings"
)

// checkGrubInstallDevices catches the "VM migrated between hypervisors"
// failure mode. grub-pc stores its target block devices in the debconf key
// `grub-pc/install_devices`, set once at install time and never updated
// automatically. A migration from VMware to Xen (sda -> xvda), or from a
// SCSI to an NVMe host, leaves the selection pointing at a device that no
// longer exists. The next grub-pc upgrade runs grub-install against that
// device and fails with `/dev/sda does not exist`, which aborts the entire
// dist-upgrade mid-dpkg.
//
// The fix is operator-driven — the correct new device depends on the
// current boot setup, and guessing it is worse than refusing. The check
// prints the exact reconfigure command and bails out.
func (app *App) checkGrubInstallDevices(ctx context.Context) {
	if app.cfg.DryRun {
		slog.Info("Running grub-pc install-devices preflight (dry-run informational)", "step", "preflight.grub_devices", "dry_run", true)
	} else {
		slog.Info("Running grub-pc install-devices preflight...", "step", "preflight.grub_devices")
	}

	if app.debconf == nil {
		slog.Warn("DebconfInspector not configured; skipping grub-pc preflight", "step", "preflight.grub_devices")

		return
	}

	out, err := app.debconf.Show(ctx, "grub-pc")
	if err != nil {
		slog.Warn("Could not read grub-pc debconf selections; skipping grub-pc preflight (debconf-utils may be missing)",
			"step", "preflight.grub_devices", "error", err.Error())

		return
	}

	devices := parseGrubInstallDevices(string(out))
	if len(devices) == 0 {
		slog.Info("grub-pc install_devices empty or not configured; nothing to verify.", "step", "preflight.grub_devices")

		return
	}

	missing := app.missingDevices(devices)
	if len(missing) == 0 {
		slog.Info("grub-pc install_devices all resolve to existing block devices.", "step", "preflight.grub_devices", "devices", devices)

		return
	}

	slog.Error("Preflight failed: grub-pc install_devices references missing block devices",
		"step", "preflight.grub_devices",
		"missing", missing,
		"configured", devices,
		"remediation", "debconf-set-selections or dpkg-reconfigure grub-pc to point at the current boot device",
	)
	app.printLines(
		"",
		"=========================================================================",
		"[FATAL ERROR] grub-pc is configured to install to block devices that",
		"no longer exist on this system. The most common cause is a VM migration",
		"that renamed the disks (VMware sda -> Xen xvda, SCSI -> NVMe, etc.).",
		"",
		"Missing devices:",
	)

	for _, d := range missing {
		app.printLines("  " + d)
	}

	app.printLines(
		"",
		"Currently configured:",
	)

	for _, d := range devices {
		app.printLines("  " + d)
	}

	app.printLines(
		"",
		"HOW TO FIX:",
		"1. Identify the current boot device with: lsblk  (look for the disk",
		"   whose first partition mounts /).",
		"2. Update grub-pc's debconf selection. Example for a Xen disk:",
		"     echo 'grub-pc grub-pc/install_devices multiselect /dev/xvda' | \\",
		"       sudo debconf-set-selections",
		"     sudo dpkg-reconfigure -f noninteractive grub-pc",
		"3. If a previous upgrade already failed at grub-pc, finish it now:",
		"     sudo dpkg --configure -a",
		"4. Re-run this tool.",
		"=========================================================================",
		"",
	)
	app.failOnError(errGrubInstallDevicesStale, "Refusing to start upgrade: grub-pc targets missing block devices")
}

// parseGrubInstallDevices extracts device paths from the `debconf-show grub-pc`
// output. The relevant key is `grub-pc/install_devices`, which is a multiselect
// of /dev/... paths separated by `, `.
func parseGrubInstallDevices(debconfOut string) []string {
	for line := range strings.SplitSeq(debconfOut, "\n") {
		trimmed := strings.TrimLeft(line, "* ")

		value, ok := strings.CutPrefix(trimmed, "grub-pc/install_devices:")
		if !ok {
			continue
		}

		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}

		var out []string

		for entry := range strings.SplitSeq(value, ",") {
			entry = strings.TrimSpace(entry)
			if entry != "" {
				out = append(out, entry)
			}
		}

		return out
	}

	return nil
}

// missingDevices returns the subset of paths that do not resolve via the
// injected FS. We only consider /dev/ paths here — symbolic values like
// LABEL=... never appear in grub-pc/install_devices but guarding anyway
// avoids spurious failures from malformed debconf output.
func (app *App) missingDevices(devices []string) []string {
	var missing []string

	for _, d := range devices {
		if !strings.HasPrefix(d, "/dev/") {
			continue
		}

		_, err := app.fs.Stat(d)
		if err != nil {
			missing = append(missing, d)
		}
	}

	return missing
}
