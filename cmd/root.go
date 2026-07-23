package cmd

import (
	"cwm/db"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cwm",
	Short: "CWM: Command Watch Manager",
	Long: `CWM: Command Watch Manager (` + Version + `)
A compiled, efficient database-driven CLI utility for developers.`,
	Version: Version,
}

func Execute() {
	// Ensure InitDB runs even when -v / --version / version is invoked
	for _, arg := range os.Args[1:] {
		if arg == "-v" || arg == "--version" || arg == "version" {
			database, err := db.InitDB()
			if err == nil {
				database.Close()
			}
			break
		}
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate("cwm " + Version + "\n")
	rootCmd.Flags().BoolP("version", "v", false, "Print the version number of CWM")
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
