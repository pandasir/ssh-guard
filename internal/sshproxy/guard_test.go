package sshproxy

import "testing"

func TestCommandGuardBlocksHighRiskCommands(t *testing.T) {
	cases := []struct {
		cmd     string
		blocked bool
	}{
		{"ls -la /var/log\r", false},
		{"rm -rf ./tmp/cache\r", false},
		{"rm -rf /\r", true},
		{"sudo rm -rf /etc\r", true},
		{"mkfs.ext4 /dev/sda1\r", true},
		{"dd if=/dev/zero of=/dev/nvme0n1 bs=4M\r", true},
		{"chmod -R 777 /\r", true},
		{"sudo reboot\r", true},
		{"iptables -F\r", true},
	}

	for _, tc := range cases {
		g := NewCommandGuard(true)
		blocked, _, _ := g.Feed([]byte(tc.cmd))
		if blocked != tc.blocked {
			t.Errorf("command %q: got blocked=%v, want %v", tc.cmd, blocked, tc.blocked)
		}
	}
}

func TestCommandGuardHandlesBackspaceAndCtrlC(t *testing.T) {
	g := NewCommandGuard(true)
	// Type rm -rf /, then Ctrl+C, then safe command
	if blocked, _, _ := g.Feed([]byte("rm -rf /")); blocked {
		t.Fatal("should not block before Enter")
	}
	if blocked, _, _ := g.Feed([]byte{0x03}); blocked {
		t.Fatal("Ctrl+C should clear line buffer")
	}
	if blocked, _, _ := g.Feed([]byte("echo hello\r")); blocked {
		t.Fatal("safe command after Ctrl+C should not be blocked")
	}
}
