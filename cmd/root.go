package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cwm",
	Short: "CWM: Command Watch Manager",
	Long: `CWM: Command Watch Manager (v2.0)
A compiled, efficient database-driven CLI utility for developers.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	// Set custom help template or settings if desired
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
