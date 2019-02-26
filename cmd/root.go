package cmd

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/routes"

	"github.com/jamesnetherton/m3u"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "iptv-proxy",
	Short: "A brief description of your application",
	Run: func(cmd *cobra.Command, args []string) {
		playlist, err := m3u.Parse(viper.GetString("m3u-url"))
		if err != nil {
			log.Fatal(err)
		}

		conf := &config.ProxyConfig{
			Playlist: &playlist,
			HostConfig: &config.HostConfiguration{
				Hostname: viper.GetString("hostname"),
				Port:     viper.GetInt64("port"),
			},
		}

		if e := routes.Serve(conf); e != nil {
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
	rootCmd.Flags().String("m3u-url", "http://example.com/iptv.m3u", "iptv m3u file")
	rootCmd.Flags().Int64("port", 8080, "Port to expose the IPTVs endpoints")
	rootCmd.Flags().String("hostname", "", "Hostname or IP to expose the IPTVs endpoints")

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
