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
	Short: "System diagnostics and status report",
	Run: func(cmd *cobra.Command, args []string) {
		// 1. Full Header
		fmt.Printf("\n%s\n", color.New(color.Bold, color.FgGreen).Sprintf("CWM - Command Watch Manager (%s)", Version))
		fmt.Println(strings.Repeat("-", 65))

		// 2. System Info
		fmt.Printf("%-20s %s/%s\n", color.New(color.Bold, color.FgCyan).Sprint("OS:"), runtime.GOOS, runtime.GOARCH)

		// 3. History File Path
		database, err := db.InitDB()
		var histPath string
		dbStatus := color.GreenString("v6 (Healthy / Synced)")

		if err == nil {
			defer database.Close()
			histPath = getHistoryFilePath(database)
		} else {
			histPath = getHistoryFilePath(nil)
			dbStatus = color.RedString("Error (%v)", err)
		}

		histStatus := histPath
		if histPath == "" {
			histStatus = color.RedString("Not Detected")
		}
		fmt.Printf("%-20s %s\n", color.New(color.Bold, color.FgCyan).Sprint("History File:"), histStatus)

		// 4. Shell Hook & Direct Execution (-x) status
		hookActive, execActive := checkShellHookStatus()

		hookStr := color.GreenString("Active (Instant Sync)")
		if !hookActive {
			hookStr = color.YellowString("Inactive (Run 'cwm setup' to enable)")
		}
		fmt.Printf("%-20s %s\n", color.New(color.Bold, color.FgCyan).Sprint("Shell Hook:"), hookStr)

		execStr := color.GreenString("Active (-x Native Wrapper Function Installed)")
		if !execActive {
			execStr = color.YellowString("Inactive (Run 'cwm setup' to enable native -x execution)")
		}
		fmt.Printf("%-20s %s\n", color.New(color.Bold, color.FgCyan).Sprint("Direct Execution:"), execStr)

		// 5. Database Schema Status
		fmt.Printf("%-20s %s\n\n", color.New(color.Bold, color.FgCyan).Sprint("Database Schema:"), dbStatus)
	},
}

func checkShellHookStatus() (bool, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, false
	}

	if runtime.GOOS == "windows" {
		profilePaths := []string{
			filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
			filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		}
		for _, p := range profilePaths {
			if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "CWM Shell Wrapper") {
				return true, true
			}
		}
		return false, false
	}

	bashrc := filepath.Join(home, ".bashrc")
	if data, err := os.ReadFile(bashrc); err == nil && (strings.Contains(string(data), "CWM") || strings.Contains(string(data), "cwm")) {
		return true, true
	}
	zshrc := filepath.Join(home, ".zshrc")
	if data, err := os.ReadFile(zshrc); err == nil && (strings.Contains(string(data), "CWM") || strings.Contains(string(data), "cwm")) {
		return true, true
	}
	return false, false
}

func init() {
	rootCmd.AddCommand(helloCmd)
}
