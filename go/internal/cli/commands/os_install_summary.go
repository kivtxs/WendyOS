package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/wendylabsinc/wendy/go/internal/cli/tui"
	"github.com/wendylabsinc/wendy/go/internal/shared/wendyconf"
)

// printProvisioningSummary shows what first boot will apply, before anything is
// erased. enrolled is nil when enrollment is settled later. Never prints a password.
func printProvisioningSummary(w io.Writer, name string, creds []wendyconf.WifiCredential, enrolled *bool) {
	fmt.Fprint(w, provisioningSummary(name, creds, enrolled, tui.Dim))
}

// logProvisioningSummary is printProvisioningSummary for a flash log header:
// unstyled, and followed by a blank line.
func logProvisioningSummary(w io.Writer, name string, creds []wendyconf.WifiCredential, enrolled *bool) {
	plain := provisioningSummary(name, creds, enrolled, func(s string) string { return s })
	fmt.Fprint(w, strings.TrimPrefix(plain, "\n")+"\n")
}

func provisioningSummary(name string, creds []wendyconf.WifiCredential, enrolled *bool, label func(string) string) string {
	var b strings.Builder
	row := func(key, value string) {
		fmt.Fprintf(&b, "  %s %s\n", label(fmt.Sprintf("%-12s", key)), value)
	}
	b.WriteString("\nFirst boot will apply:\n")
	if name == "" {
		row("Device name", "auto-generated")
	} else {
		row("Device name", tui.StripControl(name))
	}
	if len(creds) == 0 {
		row("Wi-Fi", "none — reach the board over Ethernet or USB")
	} else {
		row("Wi-Fi", strings.Join(wifiSummaryNames(creds), ", "))
	}
	if enrolled != nil {
		if *enrolled {
			row("Cloud", "pre-enrolled")
		} else {
			row("Cloud", "not enrolled")
		}
	}
	return b.String()
}

// wifiSummaryNames names each network, marking an open one so a forgotten
// password shows before the erase rather than at first boot.
func wifiSummaryNames(creds []wendyconf.WifiCredential) []string {
	names := make([]string, 0, len(creds))
	for _, c := range creds {
		n := tui.StripControl(c.SSID)
		if c.Password == "" {
			n += " (open)"
		}
		names = append(names, n)
	}
	return names
}

// provisioningWrittenLine says what landed on the config partition.
func provisioningWrittenLine(name string, creds []wendyconf.WifiCredential, enrollment bool) string {
	parts := []string{"auto-generated name"}
	if name != "" {
		parts[0] = "name " + tui.StripControl(name)
	}
	if len(creds) == 0 {
		parts = append(parts, "no Wi-Fi")
	} else {
		parts = append(parts, "Wi-Fi "+strings.Join(wifiSummaryNames(creds), ", "))
	}
	parts = append(parts, "agent seed")
	if enrollment {
		parts = append(parts, "Cloud enrollment")
	}
	return "Config partition written: " + strings.Join(parts, " · ")
}
