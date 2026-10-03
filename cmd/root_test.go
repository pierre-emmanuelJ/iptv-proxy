package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
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
	for _, name := range []string{"USER", "PASSWORD", "HOSTNAME", "PROXY_USER", "PROXY_PASSWORD", "PROXY_HOSTNAME", "PORT", "M3U_URL", "XTREAM_USER", "XTREAM_PASSWORD", "XTREAM_BASE_URL", "ADVERTISED_PORT", "HTTPS", "USER_AGENT", "XTREAM_API_GET", "XTREAM_API_GET_MOVIES", "NO_STREAM_SHARING", "GROUP_REGEX", "CHANNEL_REGEX", "GROUP_EXCLUDE_REGEX", "CHANNEL_EXCLUDE_REGEX", "LISTEN_ADDRESS", "XMLTV_URL", "MAX_CONNECTIONS", "IPTV_PROXY_CONFIG", "XTREAM_PASSTHROUGH", "HDHOMERUN_GROUP_REGEX", "HDHOMERUN_CHANNEL_REGEX", "HDHOMERUN_GROUP_EXCLUDE_REGEX", "HDHOMERUN_CHANNEL_EXCLUDE_REGEX", "STATUS_PASSWORD"} {
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
		"XMLTV_URL":             "http://guide.example/epg.xml.gz",
	})
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	want := filter.Patterns{Group: "^FR ", Channel: "HD", GroupExclude: "(?i)adult", ChannelExclude: "Backup"}
	if conf.Filter != want {
		t.Errorf("filters = %+v, want %+v", conf.Filter, want)
	}
	if conf.ListenAddress != "192.168.1.10" || conf.XMLTVURL != "http://guide.example/epg.xml.gz" {
		t.Errorf("listen address = %q, guide = %q", conf.ListenAddress, conf.XMLTVURL)
	}

	load(t, map[string]string{"CHANNEL_EXCLUDE_REGEX": "(unclosed"})
	if _, err := proxyConfig(rootCmd); err == nil || !strings.Contains(err.Error(), "--channel-exclude-regex") {
		t.Errorf("an invalid expression: err = %v", err)
	}
}

func TestProxyConfigTunerFiltersAndPassthrough(t *testing.T) {
	load(t, map[string]string{
		"HDHOMERUN_GROUP_REGEX":           "^FRANCE ",
		"HDHOMERUN_CHANNEL_REGEX":         "HD",
		"HDHOMERUN_GROUP_EXCLUDE_REGEX":   "SPORTS",
		"HDHOMERUN_CHANNEL_EXCLUDE_REGEX": "Backup",
		"XTREAM_PASSTHROUGH":              "true",
	})
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	want := filter.Patterns{Group: "^FRANCE ", Channel: "HD", GroupExclude: "SPORTS", ChannelExclude: "Backup"}
	if conf.HDHomeRunFilter != want || conf.Filter != (filter.Patterns{}) {
		t.Errorf("tuner filters = %+v, proxy filters = %+v", conf.HDHomeRunFilter, conf.Filter)
	}
	if !conf.XtreamPassthrough {
		t.Error("XTREAM_PASSTHROUGH is not read")
	}
}

func TestStatusPasswordAndVersion(t *testing.T) {
	load(t, map[string]string{"STATUS_PASSWORD": "look"})
	version := rootCmd.Version
	rootCmd.Version = "v1.2.3"
	t.Cleanup(func() { rootCmd.Version = version })
	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	if conf.StatusPassword != "look" || conf.Version != "v1.2.3" {
		t.Errorf("status password %q, version %q", conf.StatusPassword, conf.Version)
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

const usersFile = `users:
  - name: family
    password: secret
    max-connections: 2
  - name: kids
    password: other
    group-regex: "(?i)kids"
    channel-exclude-regex: "(?i)adult"
`

// The users of the configuration file, found through IPTV_PROXY_CONFIG.
func TestUsersFromTheConfigurationFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(usersFile), 0o600); err != nil {
		t.Fatal(err)
	}
	load(t, map[string]string{"IPTV_PROXY_CONFIG": file, "MAX_CONNECTIONS": "3"})
	t.Cleanup(func() { cfgFile = "" })

	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.User{
		{Name: "family", Password: "secret", MaxConnections: 2},
		{Name: "kids", Password: "other", Filter: filter.Patterns{Group: "(?i)kids", ChannelExclude: "(?i)adult"}},
	}
	if len(conf.Users) != len(want) {
		t.Fatalf("users: %+v", conf.Users)
	}
	for i := range want {
		if conf.Users[i] != want[i] {
			t.Errorf("user %d: %+v, want %+v", i, conf.Users[i], want[i])
		}
	}
	if conf.MaxConnections != 3 {
		t.Errorf("max connections = %d", conf.MaxConnections)
	}
}

func TestUsersWithAnInvalidFilter(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("users:\n  - name: kids\n    password: x\n    group-regex: \"(kids\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load(t, map[string]string{"IPTV_PROXY_CONFIG": file})
	t.Cleanup(func() { cfgFile = "" })
	if _, err := proxyConfig(rootCmd); err == nil || !strings.Contains(err.Error(), `user "kids"`) || !strings.Contains(err.Error(), "--group-regex") {
		t.Errorf("err = %v", err)
	}
}

func TestNoUsersWithoutAFile(t *testing.T) {
	load(t, map[string]string{})
	conf, err := proxyConfig(rootCmd)
	if err != nil || len(conf.Users) != 0 || conf.MaxConnections != 0 {
		t.Errorf("users %+v, max %d, err %v", conf.Users, conf.MaxConnections, err)
	}
}

func TestSourcesFromTheConfigurationFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	sources := "sources:\n  - name: backup\n    xtream-base-url: http://b.example\n    xtream-user: u\n    xtream-password: p\n    max-connections: 1\n  - xtream-base-url: http://c.example\n    xtream-user: v\n    xtream-password: q\n"
	if err := os.WriteFile(file, []byte(sources), 0o600); err != nil {
		t.Fatal(err)
	}
	load(t, map[string]string{"IPTV_PROXY_CONFIG": file})
	t.Cleanup(func() { cfgFile = "" })

	conf, err := proxyConfig(rootCmd)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Source{
		{Name: "backup", XtreamBaseURL: "http://b.example", XtreamUser: "u", XtreamPassword: "p", MaxConnections: 1},
		{XtreamBaseURL: "http://c.example", XtreamUser: "v", XtreamPassword: "q"},
	}
	if len(conf.Sources) != len(want) || conf.Sources[0] != want[0] || conf.Sources[1] != want[1] {
		t.Errorf("sources: %+v", conf.Sources)
	}
}
