package cmd

import (
	"cwm/db"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var deleteGlobalFlag bool

var bankCmd = &cobra.Command{
	Use:   "bank",
	Short: "Manage storage locations",
	Long:  `Manage storage locations, view db files information, and delete database instances.`,
}

var bankInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show database file paths and status",
	Run: func(cmd *cobra.Command, args []string) {
		database, err := db.InitDB()
		if err != nil {
			fmt.Printf(color.RedString("Database error: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		dbPath, _ := db.GetDBPath()
		copyPath, _ := db.GetConfigValue(database, "copy_bank_path")

		fmt.Println()
		fmt.Printf("  %s  %s\n", color.New(color.Bold, color.FgCyan).Sprint("Global Database Path:"), dbPath)
		if copyPath != "" {
			fmt.Printf("  %s  %s\n", color.New(color.Bold, color.FgGreen).Sprint("Copy Bank Path:       "), copyPath)
		} else {
			fmt.Println("  Copy Bank Path:        Not Configured")
		}

		if fileInfo, err := os.Stat(dbPath); err == nil {
			fmt.Printf("  %s  %.2f KB\n", color.New(color.Bold, color.FgMagenta).Sprint("Database File Size:  "), float64(fileInfo.Size())/1024.0)
		}
		fmt.Println()
	},
}

var bankDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete CWM database instances (DANGER)",
	Run: func(cmd *cobra.Command, args []string) {
		if !deleteGlobalFlag {
			fmt.Println(color.YellowString("! Please specify --global to delete the global database."))
			return
		}

		dbPath, err := db.GetDBPath()
		if err != nil {
			fmt.Printf(color.RedString("Error resolving path: %v\n"), err)
			os.Exit(1)
		}

		folderPath := filepath.Dir(dbPath)

		fmt.Printf("\n  %s You are about to DELETE the global CWM database and configurations.\n", color.New(color.Bold, color.FgRed).Sprint("WARNING:"))
		fmt.Printf("  Location: %s\n", folderPath)
		fmt.Println("  This action cannot be undone.")
		fmt.Println()

		fmt.Print("  Are you sure you want to delete it? (y/N): ")
		var confirm string
		fmt.Scanln(&confirm)
		confirm = strings.ToLower(strings.TrimSpace(confirm))

		if confirm == "y" || confirm == "yes" {
			err := os.RemoveAll(folderPath)
			if err != nil {
				fmt.Printf("\n  %s %v\n\n", color.RedString("Error deleting bank:"), err)
			} else {
				fmt.Printf("\n  %s\n\n", color.GreenString("Deleted global CWM database and folder."))
			}
		} else {
			fmt.Println("\n  Cancelled.")
		}
	},
}

func init() {
	bankDeleteCmd.Flags().BoolVar(&deleteGlobalFlag, "global", false, "Delete the global bank/database")
	bankCmd.AddCommand(bankInfoCmd)
	bankCmd.AddCommand(bankDeleteCmd)
	rootCmd.AddCommand(bankCmd)
}
