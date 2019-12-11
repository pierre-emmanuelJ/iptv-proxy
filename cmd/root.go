package cmd

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/server"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "iptv-proxy",
	Short: "Reverse proxy on iptv m3u file and xtream codes server api",
	Run: func(cmd *cobra.Command, args []string) {
		m3uURL := viper.GetString("m3u-url")
		remoteHostURL, err := url.Parse(m3uURL)
		if err != nil {
			log.Fatal(err)
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
				log.Printf("[iptv-proxy] INFO: xtream service enable with xtream base url: %q xtream username: %q xtream password: %q", xtreamBaseURL, xtreamUser, xtreamPassword)
			}
		}

		conf := &config.ProxyConfig{
			HostConfig: &config.HostConfiguration{
				Hostname: viper.GetString("hostname"),
				Port:     viper.GetInt64("port"),
			},
			RemoteURL:          remoteHostURL,
			XtreamUser:         xtreamUser,
			XtreamPassword:     xtreamPassword,
			XtreamBaseURL:      xtreamBaseURL,
			M3UCacheExpiration: viper.GetInt("m3u-cache-expiration"),
			User:               viper.GetString("user"),
			Password:           viper.GetString("password"),
			HTTPS:              viper.GetBool("https"),
			M3UFileName:        viper.GetString("m3u-file-name"),
			CustomEndpoint:     viper.GetString("custom-endpoint"),
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
	rootCmd.PersistentFlags().StringVar(&cfgFile, "iptv-proxy-config", "C", "config file (default is $HOME/.iptv-proxy.yaml)")
	rootCmd.Flags().StringP("m3u-url", "u", "", `iptv m3u file or url e.g: "http://example.com/iptv.m3u"`)
	rootCmd.Flags().StringP("m3u-file-name", "", "iptv.m3u", `name of the new proxified m3u file e.g "http://poxy.com/iptv.m3u"`)
	rootCmd.Flags().StringP("custom-endpoint", "", "", `custom endpoint "http://poxy.com/<custom-endpoint>/iptv.m3u"`)
	rootCmd.Flags().Int64("port", 8080, "Port to expose the IPTVs endpoints")
	rootCmd.Flags().String("hostname", "", "Hostname or IP to expose the IPTVs endpoints")
	rootCmd.Flags().BoolP("https", "", false, "Activate https for urls proxy")
	rootCmd.Flags().String("user", "usertest", "user UNSAFE(temp auth to access proxy)")
	rootCmd.Flags().String("password", "passwordtest", "password UNSAFE(auth to access m3u proxy and xtream proxy)")
	rootCmd.Flags().String("xtream-user", "", "Xtream-code user login")
	rootCmd.Flags().String("xtream-password", "", "Xtream-code password login")
	rootCmd.Flags().String("xtream-base-url", "", "Xtream-code base url e.g(http://expample.tv:8080)")
	rootCmd.Flags().Int("m3u-cache-expiration", 24, "M3U cache expiration in hour")

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
		home, err := homedir.Dir()
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
