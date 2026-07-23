package cmd

import (
	"cwm/db"
	"fmt"

	"github.com/spf13/cobra"
)

// Version holds the current version string of CWM.
// Sourced directly from package db.AppVersion.
const Version = db.AppVersion

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of CWM",
	Long:  `Displays the current version of the CWM (Command Watch Manager) CLI tool.`,
	Run: func(cmd *cobra.Command, args []string) {
		database, err := db.InitDB()
		if err == nil {
			defer database.Close()
		}
		fmt.Printf("cwm %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
