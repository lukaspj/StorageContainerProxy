package main

import (
	"StorageContainerProxy/pkg/proxy"
	"fmt"
	"github.com/mitchellh/go-homedir"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// Flags
	cfgFile string
	baseUrl string
)

func GetRootCmd() *cobra.Command {
	cobra.OnInitialize(initConfig)

	rootCmd := &cobra.Command{
		Use:   "scproxy",
		Short: "StorageContainerProxy is a tool for...",
		Run: func(cmd *cobra.Command, args []string) {
			h := proxy.NewHandler(&proxy.Config{
				AzureStorageAccount: "bryrupteaterfrontend",
				AzureStorageContainer: "$web",
				BaseDomain: "localhost",
			})
			h.Listen()
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.cobra.yaml)")

	return rootCmd
}

func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fatalErr(err)
		}

		// Search config in home directory with name ".cobra" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".scproxy")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

func fatalErr(msg interface{}) {
	fmt.Println("Error:", msg)
	os.Exit(1)
}
