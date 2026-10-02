package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// load reads the configuration the way the command does at startup.
func load(t *testing.T, env map[string]string) {
	t.Helper()
	// a home with no configuration file, unless the test makes one
	if _, ok := env["HOME"]; !ok {
		t.Setenv("HOME", t.TempDir())
	}
	t.Chdir(t.TempDir())
	for _, name := range []string{"USER", "PASSWORD", "HOSTNAME", "PROXY_USER", "PROXY_PASSWORD", "PROXY_HOSTNAME", "PORT", "M3U_URL", "XTREAM_USER", "XTREAM_PASSWORD", "XTREAM_BASE_URL", "ADVERTISED_PORT", "HTTPS", "USER_AGENT", "XTREAM_API_GET", "XTREAM_API_GET_MOVIES", "NO_STREAM_SHARING", "GROUP_REGEX", "CHANNEL_REGEX", "GROUP_EXCLUDE_REGEX", "CHANNEL_EXCLUDE_REGEX", "LISTEN_ADDRESS"} {
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

func TestProxyConfig(t *testing.T) {
	load(t, map[string]string{
		"M3U_URL":        "http://provider.example:8080/get.php?username=xuser&password=xpass&type=m3u_plus&output=ts",
		"PROXY_USER":     "me",
		"PROXY_PASSWORD": "secret",
		"PROXY_HOSTNAME": "tv.example",
		"PORT":           "9000",
		"HTTPS":          "1",
		"USER_AGENT":     "Player/1",
	})
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}

	// a get.php address is an Xtream account
	if conf.XtreamBaseURL != "http://provider.example:8080" || conf.XtreamUser != "xuser" || conf.XtreamPassword != "xpass" {
		t.Errorf("xtream account read from the playlist address: %q %q %q", conf.XtreamBaseURL, conf.XtreamUser, conf.XtreamPassword)
	}
	if conf.User != "me" || conf.Password != "secret" || conf.HostConfig.Hostname != "tv.example" {
		t.Errorf("proxy settings: %q %q %q", conf.User, conf.Password, conf.HostConfig.Hostname)
	}
	// the port given to players is the listening one unless said otherwise
	if conf.HostConfig.Port != 9000 || conf.AdvertisedPort != 9000 {
		t.Errorf("ports: listening %d, advertised %d", conf.HostConfig.Port, conf.AdvertisedPort)
	}
	if !conf.HTTPS || conf.UserAgent != "Player/1" || conf.M3UFileName != "iptv.m3u" || conf.M3UCacheExpiration != 1 {
		t.Errorf("other options: %+v", conf)
	}
	if conf.XtreamApiGetMovies || conf.NoStreamSharing || conf.XtreamGenerateApiGet {
		t.Errorf("an option that is off by default is on: %+v", conf)
	}
}

func TestProxyConfigExplicitXtreamAccountWins(t *testing.T) {
	load(t, map[string]string{
		"M3U_URL":         "http://other.example/get.php?username=a&password=b",
		"XTREAM_USER":     "xuser",
		"XTREAM_PASSWORD": "xpass",
		"XTREAM_BASE_URL": "http://provider.example:8080",
		"ADVERTISED_PORT": "443",
	})
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	if conf.XtreamBaseURL != "http://provider.example:8080" || conf.XtreamUser != "xuser" {
		t.Errorf("xtream account: %q %q", conf.XtreamBaseURL, conf.XtreamUser)
	}
	if conf.HostConfig.Port != 8080 || conf.AdvertisedPort != 443 {
		t.Errorf("ports: listening %d, advertised %d", conf.HostConfig.Port, conf.AdvertisedPort)
	}
}

func TestProxyConfigPlainPlaylist(t *testing.T) {
	load(t, map[string]string{"M3U_URL": "http://provider.example/list.m3u?username=a&password=b"})
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	if conf.XtreamBaseURL != "" || conf.XtreamUser != "" {
		t.Errorf("a playlist that is not get.php is not an Xtream account: %q %q", conf.XtreamBaseURL, conf.XtreamUser)
	}
	if conf.RemoteURL.String() != "http://provider.example/list.m3u?username=a&password=b" {
		t.Errorf("playlist address = %q", conf.RemoteURL)
	}

	load(t, map[string]string{"M3U_URL": "http://[broken"})
	if _, err := proxyConfig(rootCmd); err == nil {
		t.Error("an invalid playlist address is refused")
	}
}

func TestProxyConfigFilters(t *testing.T) {
	load(t, map[string]string{
		"GROUP_REGEX":           "^FR ",
		"CHANNEL_REGEX":         "HD",
		"GROUP_EXCLUDE_REGEX":   "(?i)adult",
		"CHANNEL_EXCLUDE_REGEX": "Backup",
		"LISTEN_ADDRESS":        "192.168.1.10",
	})
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	want := filter.Patterns{Group: "^FR ", Channel: "HD", GroupExclude: "(?i)adult", ChannelExclude: "Backup"}
	if conf.Filter != want {
		t.Errorf("filters = %+v, want %+v", conf.Filter, want)
	}
	if conf.ListenAddress != "192.168.1.10" {
		t.Errorf("listen address = %q", conf.ListenAddress)
	}

	load(t, map[string]string{"CHANNEL_EXCLUDE_REGEX": "(unclosed"})
	if _, err := proxyConfig(rootCmd); err == nil || !strings.Contains(err.Error(), "--channel-exclude-regex") {
		t.Errorf("an invalid expression: err = %v", err)
	}
}

// USER and HOSTNAME set by the system do not override the configuration
// file (#113); PROXY_ names and flags still do.
func TestConfigurationFileWinsOverTheSystemsVariables(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".iptv-proxy.yaml"), []byte("hostname: tv.example\nuser: fileuser\npassword: filepass\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	load(t, map[string]string{"HOME": home, "HOSTNAME": "3f2a9c1d7b21", "USER": "ubuntu", "PASSWORD": "system"})
	for option, want := range map[string]string{"hostname": "tv.example", "user": "fileuser", "password": "filepass"} {
		if got := setting(rootCmd, option); got != want {
			t.Errorf("%s = %q, want the file's %q", option, got, want)
		}
	}

	load(t, map[string]string{"HOME": home, "HOSTNAME": "3f2a9c1d7b21", "PROXY_HOSTNAME": "env.example"})
	if got := setting(rootCmd, "hostname"); got != "env.example" {
		t.Errorf("hostname = %q, want PROXY_HOSTNAME's", got)
	}

	// an option the file does not set still comes from the old name
	if err := os.WriteFile(filepath.Join(home, ".iptv-proxy.yaml"), []byte("port: 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load(t, map[string]string{"HOME": home, "USER": "old"})
	if got := setting(rootCmd, "user"); got != "old" {
		t.Errorf("user = %q, want USER's", got)
	}
}
