//go:build darwin || linux || windows

package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/wendylabsinc/wendy/go/internal/shared/wendyconf"
)

func TestProvisioningSummary(t *testing.T) {
	creds := []wendyconf.WifiCredential{
		{SSID: "Home", Password: "hunter2"},
		{SSID: "Cafe"},
		{SSID: "Off\x1b[31mice", Password: "corp-secret"},
	}
	yes, no := true, false

	var b bytes.Buffer
	printProvisioningSummary(&b, "nano-lab", creds, &yes)
	got := b.String()
	for _, want := range []string{"First boot will apply:", "Device name", "nano-lab", "Home, Cafe (open), Off[31mice", "Cloud", "pre-enrolled"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q does not contain %q", got, want)
		}
	}
	for _, secret := range []string{"hunter2", "corp-secret", "\x1b[31m"} {
		if strings.Contains(got, secret) {
			t.Errorf("summary %q leaks %q", got, secret)
		}
	}

	b.Reset()
	printProvisioningSummary(&b, "", nil, nil)
	got = b.String()
	for _, want := range []string{"auto-generated", "none — reach the board over Ethernet or USB"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty summary %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "Cloud") {
		t.Errorf("summary %q has a Cloud line although enrollment is decided later", got)
	}

	b.Reset()
	printProvisioningSummary(&b, "", nil, &no)
	if !strings.Contains(b.String(), "not enrolled") {
		t.Errorf("summary %q does not say the device is not enrolled", b.String())
	}
}

func TestLogProvisioningSummaryIsPlain(t *testing.T) {
	var b bytes.Buffer
	logProvisioningSummary(&b, "nano-lab", []wendyconf.WifiCredential{{SSID: "Home", Password: "pw"}}, nil)
	got := b.String()
	if !strings.HasPrefix(got, "First boot will apply:\n") || !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("log summary %q: want it to start with the heading and end with a blank line", got)
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "pw\n") {
		t.Fatalf("log summary %q has styling or a password", got)
	}
}

func TestProvisioningWrittenLine(t *testing.T) {
	got := provisioningWrittenLine("nano-lab", []wendyconf.WifiCredential{{SSID: "Home", Password: "hunter2"}}, true)
	want := "Config partition written: name nano-lab · Wi-Fi Home · agent seed · Cloud enrollment"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
	got = provisioningWrittenLine("", nil, false)
	want = "Config partition written: auto-generated name · no Wi-Fi · agent seed"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestProvisionConfigWithRetryReportsWhatLanded(t *testing.T) {
	creds := []wendyconf.WifiCredential{{SSID: "Home", Password: "hunter2"}}
	withStubbedProvisioning(t, func(drive, []wendyconf.WifiCredential, string, []byte) error { return nil }, false, nil)
	out, _ := captureCommandStdout(t, func() error {
		_, _, err := provisionConfigWithRetry(context.Background(), drive{}, creds, "nano-lab", nil, true, nil)
		return err
	})
	if !strings.Contains(out, "Config partition written: name nano-lab · Wi-Fi Home · agent seed") || strings.Contains(out, "hunter2") {
		t.Fatalf("output %q; want the written line without the password", out)
	}

	withStubbedProvisioning(t, func(drive, []wendyconf.WifiCredential, string, []byte) error {
		return errors.New("no config partition")
	}, false, nil)
	out, _ = captureCommandStdout(t, func() error {
		_, _, err := provisionConfigWithRetry(context.Background(), drive{}, creds, "nano-lab", nil, true, nil)
		return err
	})
	if strings.Contains(out, "Config partition written") {
		t.Fatalf("output %q claims a write that failed", out)
	}
}

func TestConfirmDiskInstallSettlesWiFiBeforeErasing(t *testing.T) {
	d := drive{Name: "SD Card", DevicePath: "/dev/disk4", IsRemovable: true}

	t.Run("undecided Wi-Fi fails before the erase question", func(t *testing.T) {
		stubWifiChoice(t, false, "")
		asked := stubInstallConfirms(t, true)
		_, _, err := confirmDiskInstall(d, wifiCLIOptions{RequireChoice: true}, "", false, false)
		if !errors.Is(err, errWiFiUndecided) || len(*asked) != 0 {
			t.Fatalf("got %v, asked %v; want errWiFiUndecided before any question", err, *asked)
		}
	})

	t.Run("bad name fails before the erase question", func(t *testing.T) {
		stubWifiChoice(t, false, "")
		asked := stubInstallConfirms(t, true)
		_, _, err := confirmDiskInstall(d, wifiCLIOptions{NoWifi: true, RequireChoice: true}, "Lab_1", true, false)
		if err == nil || !strings.Contains(err.Error(), "--device-name") || len(*asked) != 0 {
			t.Fatalf("got %v, asked %v", err, *asked)
		}
	})

	t.Run("summary is shown before the erase question", func(t *testing.T) {
		stubWifiChoice(t, true, "")
		origPrompt := installConfirmPrompt
		t.Cleanup(func() { installConfirmPrompt = origPrompt })
		installConfirmPrompt = func(q string) (bool, error) {
			_, _ = os.Stdout.WriteString("<erase question>\n")
			return true, nil
		}
		var creds []wendyconf.WifiCredential
		var name string
		out, err := captureCommandStdout(t, func() error {
			var err error
			creds, name, err = confirmDiskInstall(d, wifiCLIOptions{SSID: "Home", Password: "hunter2", RequireChoice: true}, "nano-lab", false, false)
			return err
		})
		if err != nil || name != "nano-lab" || len(creds) != 1 {
			t.Fatalf("got %+v, %q, %v", creds, name, err)
		}
		summary, question := strings.Index(out, "First boot will apply:"), strings.Index(out, "<erase question>")
		if summary < 0 || question < 0 || summary > question {
			t.Fatalf("output %q: want the summary before the erase question", out)
		}
		if strings.Contains(out, "hunter2") {
			t.Fatalf("output %q leaks the password", out)
		}
	})

	t.Run("force prints the summary and skips the question", func(t *testing.T) {
		stubWifiChoice(t, false, "")
		asked := stubInstallConfirms(t, false)
		out, err := captureCommandStdout(t, func() error {
			_, _, err := confirmDiskInstall(d, wifiCLIOptions{NoWifi: true, RequireChoice: true}, "", true, false)
			return err
		})
		if err != nil || len(*asked) != 0 || !strings.Contains(out, "auto-generated") {
			t.Fatalf("got %v, asked %v, output %q", err, *asked, out)
		}
	})

	t.Run("no terminal and no --force fails after the summary", func(t *testing.T) {
		stubWifiChoice(t, false, "")
		asked := stubInstallConfirms(t, true)
		_, err := captureCommandStdout(t, func() error {
			_, _, err := confirmDiskInstall(d, wifiCLIOptions{NoWifi: true, RequireChoice: true}, "", false, false)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "needs confirmation") || len(*asked) != 0 {
			t.Fatalf("got %v, asked %v", err, *asked)
		}
	})
}
