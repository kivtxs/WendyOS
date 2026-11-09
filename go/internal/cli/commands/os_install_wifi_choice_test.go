//go:build darwin || linux || windows

package commands

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/wendylabsinc/wendy/go/internal/cli/analytics"
)

// stubWifiChoice fakes the keychain and the Wi-Fi prompts of
// resolveWiFiCredentialsList and counts how often each is reached.
func stubWifiChoice(t *testing.T, interactive bool, keychainPW string) (keychain, prompts *int) {
	t.Helper()
	setInteractive(t, interactive)
	origKeychain, origSetup, origPassword := wifiKeychainPassword, confirmWifiSetup, promptWifiShortcutPassword
	t.Cleanup(func() {
		wifiKeychainPassword, confirmWifiSetup, promptWifiShortcutPassword = origKeychain, origSetup, origPassword
	})
	keychain, prompts = new(int), new(int)
	wifiKeychainPassword = func(string) (string, error) {
		*keychain++
		if keychainPW == "" {
			return "", errors.New("not in keychain")
		}
		return keychainPW, nil
	}
	confirmWifiSetup = func() (bool, error) { *prompts++; return false, nil }
	promptWifiShortcutPassword = func(string) (string, error) { *prompts++; return "typed-pw", nil }
	return keychain, prompts
}

func TestResolveWiFiRejectsContradictoryFlagsFirst(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts wifiCLIOptions
		want string
	}{
		{"no-wifi with password", wifiCLIOptions{NoWifi: true, Password: "pw"}, "--no-wifi cannot be combined"},
		{"no-wifi with ssid", wifiCLIOptions{NoWifi: true, SSID: "Home"}, "--no-wifi cannot be combined"},
		{"no-wifi with entry", wifiCLIOptions{NoWifi: true, Entries: []string{"ssid=Home"}}, "--no-wifi cannot be combined"},
		{"password alone", wifiCLIOptions{Password: "pw"}, "--wifi-password requires --wifi-ssid"},
		{"malformed entry", wifiCLIOptions{Entries: []string{"ssid=Home", "priority=high,ssid=Cafe"}}, "invalid --wifi"},
	} {
		for _, interactive := range []bool{false, true} {
			keychain, prompts := stubWifiChoice(t, interactive, "")
			tc.opts.RequireChoice = true
			_, err := resolveWiFiCredentialsList(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !IsUsageError(err) {
				t.Errorf("%s (interactive=%v): %v; want a usage error containing %q", tc.name, interactive, err, tc.want)
			}
			if *keychain != 0 || *prompts != 0 {
				t.Errorf("%s: consulted the keychain %d and prompted %d times before rejecting the flags", tc.name, *keychain, *prompts)
			}
		}
	}
}

func TestBadWiFiEntryErrorsNeverEchoThePassword(t *testing.T) {
	stubWifiChoice(t, false, "")
	// Zq9 and Wv7 are pieces of one password; an unescaped comma splits it.
	for _, entry := range []string{
		"ssid=Home,password=Zq9Wv7,priority=high",
		"ssid=Home,password=Zq9Wv7,hidden=maybe",
		"ssid=Home,password=Zq9,Wv7",
		"ssid=Home,password=Zq9,x=Wv7",
		"ssid=Home,pasword=Zq9Wv7",
		"password=Zq9Wv7",
	} {
		opts := wifiCLIOptions{Entries: []string{"ssid=Cafe,password=Zq9Wv7", entry}, RequireChoice: true}
		_, resolveErr := resolveWiFiCredentialsList(opts)
		for _, err := range []error{validateWiFiFlags(opts), resolveErr} {
			if err == nil || !IsUsageError(err) || !strings.Contains(err.Error(), "invalid --wifi entry 2") {
				t.Fatalf("%q: %v; want a usage error naming entry 2", entry, err)
			}
			if strings.Contains(err.Error(), "Zq9") || strings.Contains(err.Error(), "Wv7") {
				t.Fatalf("%q: error %q echoes the password", entry, err)
			}
			if strings.HasPrefix(entry, "ssid=Home") && !strings.Contains(err.Error(), `ssid "Home"`) {
				t.Errorf("%q: error %q does not name the SSID", entry, err)
			}
		}
	}
}

func TestResolveWiFiUndecidedWithoutTerminal(t *testing.T) {
	_, prompts := stubWifiChoice(t, false, "")

	creds, err := resolveWiFiCredentialsList(wifiCLIOptions{RequireChoice: true})
	if !errors.Is(err, errWiFiUndecided) || creds != nil {
		t.Fatalf("got %v, %v; want errWiFiUndecided", creds, err)
	}
	for _, flag := range []string{"--wifi ssid=", "--wifi-ssid", "--no-wifi"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("error %q does not name %s", err, flag)
		}
	}
	if *prompts != 0 {
		t.Fatal("prompted without a terminal")
	}
}

func TestResolveWiFiExplicitNoneWithoutTerminal(t *testing.T) {
	stubWifiChoice(t, false, "")
	creds, err := resolveWiFiCredentialsList(wifiCLIOptions{NoWifi: true, RequireChoice: true})
	if err != nil || creds != nil {
		t.Fatalf("--no-wifi: got %v, %v; want nil, nil", creds, err)
	}
}

func TestResolveWiFiOptionalCallerKeepsNoneWithoutTerminal(t *testing.T) {
	// Callers without Wi-Fi flags (wendy run reinstalling Wendy Lite) cannot act on the error.
	stubWifiChoice(t, false, "")
	creds, err := resolveWiFiCredentialsList(wifiCLIOptions{})
	if err != nil || creds != nil {
		t.Fatalf("got %v, %v; want nil, nil", creds, err)
	}
}

func TestResolveWiFiSSIDWithoutPassword(t *testing.T) {
	t.Run("keychain has it", func(t *testing.T) {
		stubWifiChoice(t, false, "saved-pw")
		creds, err := resolveWiFiCredentialsList(wifiCLIOptions{SSID: "Home", RequireChoice: true})
		if err != nil || len(creds) != 1 || creds[0].Password != "saved-pw" {
			t.Fatalf("got %+v, %v; want the keychain password", creds, err)
		}
	})
	t.Run("no terminal and no keychain entry", func(t *testing.T) {
		keychain, prompts := stubWifiChoice(t, false, "")
		_, err := resolveWiFiCredentialsList(wifiCLIOptions{SSID: "Home", RequireChoice: true})
		if err == nil || !IsUsageError(err) {
			t.Fatalf("got %v; want a usage error instead of an open network", err)
		}
		for _, want := range []string{`"Home"`, "--wifi-password", "--wifi ssid=Home"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %s", err, want)
			}
		}
		if *keychain != 1 || *prompts != 0 {
			t.Fatalf("keychain %d, prompts %d; want 1 and 0", *keychain, *prompts)
		}
	})
	t.Run("terminal asks", func(t *testing.T) {
		_, prompts := stubWifiChoice(t, true, "")
		creds, err := resolveWiFiCredentialsList(wifiCLIOptions{SSID: "Home", RequireChoice: true})
		if err != nil || len(creds) != 1 || creds[0].Password != "typed-pw" || *prompts != 1 {
			t.Fatalf("got %+v, %v after %d prompts; want the typed password", creds, err, *prompts)
		}
	})
	t.Run("open network by entry", func(t *testing.T) {
		keychain, _ := stubWifiChoice(t, false, "")
		creds, err := resolveWiFiCredentialsList(wifiCLIOptions{Entries: []string{"ssid=Cafe"}, RequireChoice: true})
		if err != nil || len(creds) != 1 || creds[0].Password != "" || *keychain != 0 {
			t.Fatalf("got %+v, %v; want one open network", creds, err)
		}
	})
}

func TestResolveWiFiInteractiveDeclineIsExplicitNone(t *testing.T) {
	_, prompts := stubWifiChoice(t, true, "")
	creds, err := resolveWiFiCredentialsList(wifiCLIOptions{RequireChoice: true})
	if err != nil || creds != nil || *prompts != 1 {
		t.Fatalf("got %v, %v after %d prompts; want nil, nil after one question", creds, err, *prompts)
	}
}

// The Dragonwing path reaches the Wi-Fi choice with only manifests fetched;
// serveManifests fails the test on any other request, such as the bundle.
func TestInstallCommandWithoutTerminalNeedsAWiFiChoiceBeforeDownloading(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	stubSessionTerminals(t, false, false, nil)
	// An earlier test may have enabled telemetry, which would post through the stub.
	analytics.Disable()
	serveManifests(t, map[string]string{
		"manifests/master.json": `{"devices":{"dragonwing-iq-9075":{"latest":"1.0.0","manifest_path":"manifests/iq9.json"}}}`,
		"manifests/iq9.json":    `{"versions":{"1.0.0":{"qcomflash_path":"iq9/bundle.tar.gz","qcomflash_checksum":"abc123","qcomflash_size_bytes":1024}}}`,
	})
	run := func(args ...string) error {
		cmd := newOSInstallCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{"--device-type", "dragonwing-iq-9075"}, args...))
		return cmd.Execute()
	}

	if err := run(); !errors.Is(err, errWiFiUndecided) {
		t.Fatalf("no Wi-Fi flag: %v; want errWiFiUndecided", err)
	}
	// Past the Wi-Fi choice, the run stops at the next question nobody can answer.
	if err := run("--no-wifi"); err == nil || errors.Is(err, errWiFiUndecided) || !strings.Contains(err.Error(), "pass --force") {
		t.Fatalf("--no-wifi: %v; want the Wi-Fi choice settled and the confirmation refused", err)
	}
}
