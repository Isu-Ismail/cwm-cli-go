package cmd

import (
	"cwm/db"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var helloCmd = &cobra.Command{
	Use:   "hello",
	Short: "System diagnostics",
	Run: func(cmd *cobra.Command, args []string) {
		// 1. Version
		fmt.Printf("%s\n", color.New(color.Bold, color.FgGreen).Sprint("CWM v2.0"))

		// 2. System Info
		fmt.Printf("%s  %s %s\n", color.New(color.Bold, color.FgBlue).Sprint("System:"), runtime.GOOS, runtime.GOARCH)

		// 3. History File path
		database, err := db.InitDB()
		var histPath string
		if err == nil {
			defer database.Close()
			histPath = getHistoryFilePath(database)
		} else {
			histPath = getHistoryFilePath(nil)
		}

		histStatus := color.New(color.FgWhite).Sprint(histPath)
		if histPath == "" {
			histStatus = color.New(color.FgRed).Sprint("Not Detected")
		}
		fmt.Printf("%s %s\n", color.New(color.Bold, color.FgBlue).Sprint("History:"), histStatus)

		// 4. Sync Warning (Notice)
		if runtime.GOOS != "windows" {
			if !isHistorySyncEnabledUnix() {
				fmt.Printf("%s\n", color.New(color.FgYellow).Sprint("! Notice: Real-time sync not enabled (Run 'cwm setup' on Linux/Mac)."))
			}
		}
		fmt.Println()
	},
}

func isHistorySyncEnabledUnix() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	// Check .bashrc
	bashrc := filepath.Join(home, ".bashrc")
	if data, err := os.ReadFile(bashrc); err == nil && strings.Contains(string(data), "CWM History Setup") {
		return true
	}
	// Check .zshrc
	zshrc := filepath.Join(home, ".zshrc")
	if data, err := os.ReadFile(zshrc); err == nil && strings.Contains(string(data), "CWM History Setup") {
		return true
	}
	return false
}

func init() {
	rootCmd.AddCommand(helloCmd)
}
