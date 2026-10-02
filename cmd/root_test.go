package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// load reads the configuration the way the command does at startup.
func load(t *testing.T, env map[string]string) {
	t.Helper()
	// a home with no configuration file, unless the test makes one
	if _, ok := env["HOME"]; !ok {
		t.Setenv("HOME", t.TempDir())
	}
	t.Chdir(t.TempDir())
	for _, name := range []string{"USER", "PASSWORD", "HOSTNAME", "PROXY_USER", "PROXY_PASSWORD", "PROXY_HOSTNAME", "PORT"} {
		t.Setenv(name, "")
		os.Unsetenv(name) // nolint: errcheck
	}
	for name, value := range env {
		t.Setenv(name, value)
	}

	viper.Reset()
	if err := viper.BindPFlags(rootCmd.Flags()); err != nil {
		t.Fatal(err)
	}
	// as when the option is not given on the command line
	cfgFile = rootCmd.PersistentFlags().Lookup("iptv-proxy-config").DefValue
	initConfig()
}

// USER and HOSTNAME are set by shells and by Docker: the PROXY_ names are
// the unambiguous ones, and the old names keep working.
func TestEnvironmentNames(t *testing.T) {
	for name, c := range map[string]struct {
		env                      map[string]string
		user, password, hostname string
	}{
		"nothing set: the defaults": {
			env:  map[string]string{},
			user: "usertest", password: "passwordtest", hostname: "",
		},
		"the old names": {
			env:  map[string]string{"USER": "old", "PASSWORD": "oldpass", "HOSTNAME": "old.example"},
			user: "old", password: "oldpass", hostname: "old.example",
		},
		"the new names": {
			env:  map[string]string{"PROXY_USER": "me", "PROXY_PASSWORD": "secret", "PROXY_HOSTNAME": "tv.example"},
			user: "me", password: "secret", hostname: "tv.example",
		},
		"both: the new names win over what the system set": {
			env:  map[string]string{"USER": "ubuntu", "HOSTNAME": "3f2a9c1d7b21", "PROXY_USER": "me", "PROXY_HOSTNAME": "tv.example", "PASSWORD": "oldpass"},
			user: "me", password: "oldpass", hostname: "tv.example",
		},
	} {
		load(t, c.env)
		if got := setting(rootCmd, "user"); got != c.user {
			t.Errorf("%s: user = %q, want %q", name, got, c.user)
		}
		if got := setting(rootCmd, "password"); got != c.password {
			t.Errorf("%s: password = %q, want %q", name, got, c.password)
		}
		if got := setting(rootCmd, "hostname"); got != c.hostname {
			t.Errorf("%s: hostname = %q, want %q", name, got, c.hostname)
		}
	}
}

func TestAFlagWinsOverTheEnvironment(t *testing.T) {
	load(t, map[string]string{"USER": "ubuntu", "PROXY_USER": "me"})
	if err := rootCmd.Flags().Set("user", "fromflag"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f := rootCmd.Flags().Lookup("user")
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	if got := setting(rootCmd, "user"); got != "fromflag" {
		t.Errorf("user = %q, want the flag's", got)
	}
}

func TestOtherOptionsStillComeFromTheEnvironment(t *testing.T) {
	load(t, map[string]string{"PORT": "9000", "XTREAM_API_GET_MOVIES": "1", "NO_STREAM_SHARING": "true"})
	if viper.GetInt("port") != 9000 || !viper.GetBool("xtream-api-get-movies") || !viper.GetBool("no-stream-sharing") {
		t.Errorf("port=%d movies=%v no-sharing=%v", viper.GetInt("port"), viper.GetBool("xtream-api-get-movies"), viper.GetBool("no-stream-sharing"))
	}
}

// Without --iptv-proxy-config, .iptv-proxy.yaml is looked for in the home
// directory, as the option's help says.
func TestConfigurationFileInTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".iptv-proxy.yaml"), []byte("port: 9999\nxtream-user: fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	load(t, map[string]string{"HOME": home})
	if got := viper.GetInt("port"); got != 9999 {
		t.Errorf("port = %d, want the file's 9999", got)
	}
	if got := viper.GetString("xtream-user"); got != "fromfile" {
		t.Errorf("xtream-user = %q", got)
	}

	// the environment still wins over the file
	load(t, map[string]string{"HOME": home, "PORT": "7000"})
	if got := viper.GetInt("port"); got != 7000 {
		t.Errorf("port = %d, want the environment's 7000", got)
	}
}
