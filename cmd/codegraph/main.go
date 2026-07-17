package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	verbose   bool
	neo4jURI  string
	neo4jUser string
	neo4jPass string
	neo4jDB   string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "codegraph",
	Short: "Code Intelligence Platform CLI",
	Long: `CodeGraph is a CLI tool for building and querying a comprehensive code intelligence platform
using Neo4j as the backend graph database. It creates a Code Property Graph (CPG) that captures
syntactic structure, semantic relationships, control flow, data flow, and connections to business
requirements.`,
}

// Execute adds all child commands to the root command and sets flags appropriately
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.codegraph.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().StringVar(&neo4jURI, "neo4j-uri", "bolt://localhost:7687", "Neo4j connection URI")
	rootCmd.PersistentFlags().StringVar(&neo4jUser, "neo4j-user", "neo4j", "Neo4j username")
	rootCmd.PersistentFlags().StringVar(&neo4jPass, "neo4j-password", "password123", "Neo4j password")
	rootCmd.PersistentFlags().StringVar(&neo4jDB, "neo4j-database", "neo4j", "Neo4j database name")

	// Bind flags to viper
	viper.BindPFlag("neo4j.uri", rootCmd.PersistentFlags().Lookup("neo4j-uri"))
	viper.BindPFlag("neo4j.username", rootCmd.PersistentFlags().Lookup("neo4j-user"))
	viper.BindPFlag("neo4j.password", rootCmd.PersistentFlags().Lookup("neo4j-password"))
	viper.BindPFlag("neo4j.database", rootCmd.PersistentFlags().Lookup("neo4j-database"))
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
}

func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".codegraph" (without extension)
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".codegraph")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil && verbose {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

func main() {
	Execute()
}
