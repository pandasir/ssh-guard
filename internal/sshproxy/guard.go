package sshproxy

import (
	"regexp"
	"strings"
)

type GuardRule struct {
	Name    string
	Pattern *regexp.Regexp
}

// DefaultHighRiskRules catches common catastrophic shell commands.
var DefaultHighRiskRules = []GuardRule{
	{Name: "强制递归删除根目录或关键路径", Pattern: regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+(/(\s|$|\*)|/\*|/etc\b|/usr\b|/var\b|/boot\b|/home\b|~(\s|$))`)},
	{Name: "格式化磁盘设备 (mkfs)", Pattern: regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`)},
	{Name: "底层块设备覆盖 (dd)", Pattern: regexp.MustCompile(`(?i)\bdd\s+.*\bof=/dev/(sd|vd|nvme|hd|xvd|mmcblk)`)},
	{Name: "直接写入磁盘设备", Pattern: regexp.MustCompile(`>\s*/dev/(sd|vd|nvme|hd|xvd)`)},
	{Name: "Fork 炸弹", Pattern: regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)},
	{Name: "全局 777 权限递归修改", Pattern: regexp.MustCompile(`(?i)\bchmod\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)?0?777\s+/(\s|$)`)},
	{Name: "关机或重启指令", Pattern: regexp.MustCompile(`(?i)(^|[;&|]\s*)(sudo\s+)?(shutdown|reboot|poweroff|halt|init\s+[06])\b`)},
	{Name: "清空 iptables 防火墙规则", Pattern: regexp.MustCompile(`(?i)\biptables\s+(-[a-zA-Z]+\s+)*-F\b`)},
}

// CommandGuard maintains an approximate current command line buffer and checks
// against high-risk rules whenever Enter (\r or \n) is pressed.
type CommandGuard struct {
	enabled bool
	rules   []GuardRule
	buf     []rune
	inEsc   bool
}

func NewCommandGuard(enabled bool) *CommandGuard {
	return &CommandGuard{
		enabled: enabled,
		rules:   DefaultHighRiskRules,
	}
}

// Feed processes raw terminal input bytes from the browser.
// Returns (blocked, ruleName, matchedCommand).
func (g *CommandGuard) Feed(data []byte) (bool, string, string) {
	if !g.enabled {
		return false, "", ""
	}
	for _, r := range string(data) {
		// Ignore ANSI escape sequences (e.g., arrow keys \x1b[A)
		if g.inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '~' {
				g.inEsc = false
			}
			continue
		}
		if r == 0x1b { // ESC
			g.inEsc = true
			continue
		}

		switch r {
		case '\r', '\n':
			cmd := strings.TrimSpace(string(g.buf))
			g.buf = g.buf[:0]
			if cmd != "" {
				if ruleName, hit := g.match(cmd); hit {
					return true, ruleName, cmd
				}
			}
		case 0x03, 0x15: // Ctrl+C, Ctrl+U clear line
			g.buf = g.buf[:0]
		case 0x7f, 0x08: // Backspace
			if len(g.buf) > 0 {
				g.buf = g.buf[:len(g.buf)-1]
			}
		default:
			if r >= 0x20 && r != 0x7f && len(g.buf) < 4096 {
				g.buf = append(g.buf, r)
			}
		}
	}
	return false, "", ""
}

func (g *CommandGuard) match(cmd string) (string, bool) {
	for _, rule := range g.rules {
		if rule.Pattern.MatchString(cmd) {
			return rule.Name, true
		}
	}
	return "", false
}
