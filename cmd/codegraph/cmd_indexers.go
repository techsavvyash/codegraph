package main

import (
	"fmt"
	"strings"

	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/spf13/cobra"
)

// indexersCmd is the parent command for indexer management.
var indexersCmd = &cobra.Command{
	Use:   "indexers",
	Short: "Manage SCIP indexer binaries",
	Long:  "Download, cache, and manage SCIP indexer binaries for supported languages",
}

// indexersInstallCmd installs SCIP indexer binaries.
var indexersInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install SCIP indexer binaries",
	Long:  "Download and cache SCIP indexer binaries for specified languages",
	RunE: func(cmd *cobra.Command, args []string) error {
		langStr, _ := cmd.Flags().GetString("language")
		cacheDir, _ := cmd.Flags().GetString("cache-dir")

		mgr := static.NewIndexerManager(cacheDir)

		var languages []static.Language
		if langStr != "" {
			for _, l := range strings.Split(langStr, ",") {
				languages = append(languages, static.Language(strings.TrimSpace(l)))
			}
		} else {
			// Install all known languages
			languages = []static.Language{
				static.LanguageGo,
				static.LanguageTypeScript,
				static.LanguagePython,
				static.LanguageJava,
			}
		}

		installed, failed := mgr.InstallAll(languages)
		if len(installed) > 0 {
			fmt.Printf("Installed/verified: %d indexers\n", len(installed))
			for _, lang := range installed {
				fmt.Printf("  %s: %s\n", lang, mgr.ResolveBinary(lang))
			}
		}
		if len(failed) > 0 {
			fmt.Printf("Failed: %d indexers\n", len(failed))
			for lang, err := range failed {
				fmt.Printf("  %s: %v\n", lang, err)
			}
		}
		return nil
	},
}

// indexersStatusCmd shows the status of installed SCIP indexer binaries.
var indexersStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show indexer installation status",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := static.NewIndexerManager("")
		statuses := mgr.Status()

		fmt.Println("SCIP Indexer Status:")
		fmt.Println("====================")
		for _, s := range statuses {
			status := "NOT INSTALLED"
			if s.Installed {
				status = fmt.Sprintf("installed (%s)", s.Path)
			}
			fmt.Printf("  %-12s %-20s %s %s\n", s.Language, s.Binary, s.Version, status)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(indexersCmd)

	indexersCmd.AddCommand(indexersInstallCmd)
	indexersCmd.AddCommand(indexersStatusCmd)

	indexersInstallCmd.Flags().String("language", "", "Comma-separated languages to install (e.g., go,typescript,python)")
	indexersInstallCmd.Flags().String("cache-dir", "", "Custom cache directory for indexer binaries")
}
