package main

import (
	"testing"
)

func TestIsUnsafeDevice(t *testing.T) {
	tests := []struct {
		device string
		want   bool
	}{
		{"/dev/sda1", true},
		{"/dev/vda1", true},
		{"/dev/mapper/root", false},
		{"/dev/disk/by-uuid/123", false},
		{"/dev/md0", false},
		{"/dev/zvol/tank/root", false},
		{"UUID=123", false},
		{"LABEL=boot", false},
	}

	for _, tt := range tests {
		if got := isUnsafeDevice(tt.device); got != tt.want {
			t.Errorf("isUnsafeDevice(%q) = %v, want %v", tt.device, got, tt.want)
		}
	}
}

func TestIndexOf(t *testing.T) {
	arr := []string{"a", "b", "c"}
	tests := []struct {
		val  string
		want int
	}{
		{"a", 0},
		{"b", 1},
		{"c", 2},
		{"d", -1},
	}

	for _, tt := range tests {
		if got := indexOf(tt.val, arr); got != tt.want {
			t.Errorf("indexOf(%q, %v) = %v, want %v", tt.val, arr, got, tt.want)
		}
	}
}

func TestRepoOriginalName(t *testing.T) {
	tests := []struct {
		disabled string
		want     string
		wantErr  bool
	}{
		{"/etc/apt/sources.list.d/docker.list.disabled_by_updater", "/etc/apt/sources.list.d/docker.list", false},
		{"/etc/apt/sources.list.d/nodesource.sources.disabled_by_updater", "/etc/apt/sources.list.d/nodesource.sources", false},
		{"/tmp/unsafe.list.disabled_by_updater", "", true},
		{"/etc/apt/sources.list.d/normal.list", "", true},
	}

	for _, tt := range tests {
		got, err := repoOriginalName(tt.disabled)
		if (err != nil) != tt.wantErr {
			t.Errorf("repoOriginalName(%q) error = %v, wantErr %v", tt.disabled, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("repoOriginalName(%q) = %q, want %q", tt.disabled, got, tt.want)
		}
	}
}

func TestPatchRepoContent(t *testing.T) {
	releases := []string{"jessie", "stretch", "buster", "bullseye", "bookworm"}
	tests := []struct {
		content       string
		finalCodename string
		want          string
	}{
		{
			"deb http://download.docker.com/linux/debian buster stable",
			"bookworm",
			"deb http://download.docker.com/linux/debian bookworm stable",
		},
		{
			"deb http://repo.mysql.com/apt/debian/ stretch mysql-5.7",
			"bullseye",
			"deb http://repo.mysql.com/apt/debian/ bullseye mysql-5.7",
		},
		{
			"deb http://packages.microsoft.com/repos/edge stable main",
			"bookworm",
			"deb http://packages.microsoft.com/repos/edge stable main", // No release name found
		},
	}

	for _, tt := range tests {
		if got := patchRepoContent(tt.content, tt.finalCodename, releases); got != tt.want {
			t.Errorf("patchRepoContent(%q, %q) = %q, want %q", tt.content, tt.finalCodename, got, tt.want)
		}
	}
}
