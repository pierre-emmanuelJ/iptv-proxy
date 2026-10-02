/*
 * Iptv-Proxy is a project to proxyfie an m3u file and to proxyfie an Xtream iptv service (client API).
 * Copyright (C) 2020  Pierre-Emmanuel Jacquier
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package cmd

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/server"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "iptv-proxy",
	Short: "Reverse proxy on iptv m3u file and xtream codes server api",
	Run: func(cmd *cobra.Command, args []string) {
		conf, err := proxyConfig(cmd)
		if err != nil {
			log.Fatal(err)
		}

		// gin's debug mode lists the routes at startup, and the stream
		// routes hold the proxy's user and password.
		if os.Getenv(gin.EnvGinMode) == "" {
			gin.SetMode(gin.ReleaseMode)
		}

		server, err := server.NewServer(conf)
		if err != nil {
			log.Fatal(err)
		}

		if e := server.Serve(); e != nil {
			log.Fatal(e)
		}
	},
}

// proxyConfig builds the proxy's configuration from the flags, the
// environment and the configuration file.
func proxyConfig(cmd *cobra.Command) (*config.ProxyConfig, error) {
	m3uURL := viper.GetString("m3u-url")
	remoteHostURL, err := url.Parse(m3uURL)
	if err != nil {
		return nil, errors.New("invalid --m3u-url")
	}

	xtreamUser := viper.GetString("xtream-user")
	xtreamPassword := viper.GetString("xtream-password")
	xtreamBaseURL := viper.GetString("xtream-base-url")

	var username, password string
	if strings.Contains(m3uURL, "/get.php") {
		username = remoteHostURL.Query().Get("username")
		password = remoteHostURL.Query().Get("password")
	}

	if xtreamBaseURL == "" && xtreamPassword == "" && xtreamUser == "" {
		if username != "" && password != "" {
			log.Printf("[iptv-proxy] INFO: It's seams you are using an Xtream provider!")

			xtreamUser = username
			xtreamPassword = password
			xtreamBaseURL = fmt.Sprintf("%s://%s", remoteHostURL.Scheme, remoteHostURL.Host)
			log.Printf("[iptv-proxy] INFO: xtream service enable with xtream base url: %q xtream username: %q", xtreamBaseURL, xtreamUser)
		}
	}

	conf := &config.ProxyConfig{
		HostConfig: &config.HostConfiguration{
			Hostname: setting(cmd, "hostname"),
			Port:     viper.GetInt("port"),
		},
		RemoteURL:            remoteHostURL,
		XtreamUser:           config.CredentialString(xtreamUser),
		XtreamPassword:       config.CredentialString(xtreamPassword),
		XtreamBaseURL:        xtreamBaseURL,
		M3UCacheExpiration:   viper.GetInt("m3u-cache-expiration"),
		User:                 config.CredentialString(setting(cmd, "user")),
		Password:             config.CredentialString(setting(cmd, "password")),
		AdvertisedPort:       viper.GetInt("advertised-port"),
		HTTPS:                viper.GetBool("https"),
		M3UFileName:          viper.GetString("m3u-file-name"),
		CustomEndpoint:       viper.GetString("custom-endpoint"),
		CustomId:             viper.GetString("custom-id"),
		XtreamGenerateApiGet: viper.GetBool("xtream-api-get"),
		XtreamApiGetMovies:   viper.GetBool("xtream-api-get-movies"),
		UserAgent:            viper.GetString("user-agent"),
		NoStreamSharing:      viper.GetBool("no-stream-sharing"),
		Filter: filter.Patterns{
			Group:          viper.GetString("group-regex"),
			Channel:        viper.GetString("channel-regex"),
			GroupExclude:   viper.GetString("group-exclude-regex"),
			ChannelExclude: viper.GetString("channel-exclude-regex"),
		},
		ListenAddress: viper.GetString("listen-address"),
	}
	// An invalid expression is reported before anything starts.
	if _, err := filter.New(conf.Filter); err != nil {
		return nil, err
	}

	if conf.AdvertisedPort == 0 {
		conf.AdvertisedPort = conf.HostConfig.Port
	}

	return conf, nil
}

// setting reads one of the options whose environment variable is also an
// ordinary system variable: a shell sets USER to the login name, Docker sets
// HOSTNAME to the container's id. What was meant wins over what the system
// set: a flag given on the command line, then PROXY_USER, PROXY_PASSWORD or
// PROXY_HOSTNAME, then the configuration file, and only then the old names,
// which keep working.
func setting(cmd *cobra.Command, option string) string {
	if cmd.Flags().Changed(option) {
		return viper.GetString(option)
	}
	if value := os.Getenv("PROXY_" + strings.ToUpper(option)); value != "" {
		return value
	}
	if value, ok := fileSetting(option); ok {
		return value
	}
	return viper.GetString(option)
}

// fileSetting reads an option from the configuration file alone: viper puts
// the environment above the file.
func fileSetting(option string) (string, bool) {
	if viper.ConfigFileUsed() == "" || !viper.InConfig(option) {
		return "", false
	}
	file := viper.New()
	file.SetConfigFile(viper.ConfigFileUsed())
	if err := file.ReadInConfig(); err != nil {
		return "", false
	}
	return file.GetString(option), true
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "iptv-proxy-config", "", "Config file (default is .iptv-proxy.yaml in the home or current directory)")
	rootCmd.Flags().StringP("m3u-url", "u", "", `Iptv m3u file or url e.g: "http://example.com/iptv.m3u"`)
	rootCmd.Flags().StringP("m3u-file-name", "", "iptv.m3u", `Name of the new proxified m3u file e.g "http://poxy.com/iptv.m3u"`)
	rootCmd.Flags().StringP("custom-endpoint", "", "", `Custom endpoint "http://poxy.com/<custom-endpoint>/iptv.m3u"`)
	rootCmd.Flags().StringP("custom-id", "", "", `Custom anti-collison ID for each track "http://proxy.com/<custom-id>/..."`)
	rootCmd.Flags().Int("port", 8080, "Iptv-proxy listening port")
	rootCmd.Flags().String("listen-address", "", "IP address to listen on (default: every interface)")
	rootCmd.Flags().Int("advertised-port", 0, "Port to expose the IPTV file and xtream (by default, it's taking value from port) useful to put behind a reverse proxy")
	rootCmd.Flags().String("hostname", "", "Hostname or IP to expose the IPTVs endpoints")
	rootCmd.Flags().BoolP("https", "", false, "Activate https for urls proxy")
	rootCmd.Flags().String("user", "usertest", "User auth to access proxy (m3u/xtream)")
	rootCmd.Flags().String("password", "passwordtest", "Password auth to access proxy (m3u/xtream)")
	rootCmd.Flags().String("xtream-user", "", "Xtream-code user login")
	rootCmd.Flags().String("xtream-password", "", "Xtream-code password login")
	rootCmd.Flags().String("xtream-base-url", "", "Xtream-code base url e.g(http://expample.tv:8080)")
	rootCmd.Flags().Int("m3u-cache-expiration", 1, "M3U cache expiration in hour")
	rootCmd.Flags().BoolP("xtream-api-get", "", false, "Generate get.php from xtream API instead of get.php original endpoint")
	rootCmd.Flags().Bool("xtream-api-get-movies", false, "Add the provider's movies to the playlist generated from the xtream API (large catalogues make a playlist some players cannot load)")
	rootCmd.Flags().String("user-agent", "", "User-Agent sent to the provider instead of the client's (some providers only answer known players)")
	rootCmd.Flags().Bool("no-stream-sharing", false, "Open one provider connection per client for a live stream, instead of sharing one between the clients watching it")
	rootCmd.Flags().String("group-regex", "", `Keep only the live channels whose group matches this regular expression, e.g. "^(FR|UK) "`)
	rootCmd.Flags().String("channel-regex", "", `Keep only the live channels whose name matches this regular expression, e.g. "HD$"`)
	rootCmd.Flags().String("group-exclude-regex", "", `Leave out the live channels whose group matches this regular expression, e.g. "(?i)adult"`)
	rootCmd.Flags().String("channel-exclude-regex", "", `Leave out the live channels whose name matches this regular expression`)

	if e := viper.BindPFlags(rootCmd.Flags()); e != nil {
		log.Fatal("error binding PFlags to viper")
	}
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".iptv-proxy" (without extension).
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigName(".iptv-proxy")
	}

	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}
