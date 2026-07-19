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

var (
	setConfigFlag   string
	showConfigFlag  bool
	clearConfigFlag bool
	copyBankFlag    string
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration settings",
	Long:  `View, set, or clear configuration keys in the database.`,
	Run: func(cmd *cobra.Command, args []string) {
		database, err := db.InitDB()
		if err != nil {
			fmt.Printf(color.RedString("Database error: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		if cmd.Flags().Changed("copy-bank") {
			val := strings.TrimSpace(copyBankFlag)
			if val == "" || strings.ToLower(val) == "clear" || strings.ToLower(val) == "none" {
				err = db.SetConfigValue(database, "copy_bank_path", "")
				fmt.Println(color.GreenString("Copy bank path cleared."))
			} else {
				absVal, absErr := filepath.Abs(val)
				if absErr == nil {
					val = filepath.ToSlash(absVal)
				}
				err = db.SetConfigValue(database, "copy_bank_path", val)
				fmt.Printf(color.GreenString("Set copy bank path: ")+"%s\n", val)
			}

			if err != nil {
				fmt.Printf(color.RedString("Error writing configuration: %v\n"), err)
				os.Exit(1)
			}
			return
		}

		if setConfigFlag != "" {
			if !strings.Contains(setConfigFlag, "=") {
				fmt.Println(color.RedString("Error: Format must be key=value (e.g. --set copy_bank_path=\"/path\")"))
				os.Exit(1)
			}
			parts := strings.SplitN(setConfigFlag, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			// Resolve copy_bank_path to absolute if configured
			if key == "copy_bank_path" {
				if val == "" || strings.ToLower(val) == "clear" || strings.ToLower(val) == "none" {
					val = ""
				} else {
					absVal, absErr := filepath.Abs(val)
					if absErr == nil {
						val = filepath.ToSlash(absVal)
					}
				}
			}

			if val == "" {
				err = db.SetConfigValue(database, key, "")
				fmt.Printf(color.GreenString("Configuration key '%s' cleared.\n"), key)
			} else {
				err = db.SetConfigValue(database, key, val)
				fmt.Printf(color.GreenString("Set configuration: ")+"%s = %s\n", key, val)
			}

			if err != nil {
				fmt.Printf(color.RedString("Error writing configuration: %v\n"), err)
				os.Exit(1)
			}
			return
		}

		if clearConfigFlag {
			if err := db.ClearConfig(database); err != nil {
				fmt.Printf(color.RedString("Error clearing config: %v\n"), err)
				os.Exit(1)
			}
			fmt.Println(color.GreenString("All configurations cleared."))
			return
		}

		if showConfigFlag || len(args) == 0 {
			// Show Config
			dbPath, _ := db.GetDBPath()
			copyPath, _ := db.GetConfigValue(database, "copy_bank_path")
			histFile, _ := db.GetConfigValue(database, "history_file")
			theme, _ := db.GetConfigValue(database, "code_theme")

			if theme == "" {
				theme = "monokai"
			}
			if copyPath == "" {
				copyPath = "Not Configured"
			}
			if histFile == "" {
				histFile = "Auto-Detect"
			}

			fmt.Println()
			fmt.Println(color.New(color.Bold, color.FgBlue).Sprint("Configuration Source"))
			fmt.Printf("  Path: %s\n\n", dbPath)

			fmt.Println(color.New(color.Bold, color.FgGreen).Sprint("General Settings"))
			fmt.Printf("  History File:   %s\n", histFile)
			fmt.Printf("  Code Theme:     %s\n", theme)
			fmt.Printf("  Copy Bank Path: %s\n\n", copyPath)
			return
		}
	},
}

func init() {
	configCmd.Flags().StringVar(&setConfigFlag, "set", "", "Set configuration key=value")
	configCmd.Flags().BoolVar(&showConfigFlag, "show", false, "Show current configuration settings")
	configCmd.Flags().BoolVar(&clearConfigFlag, "clear", false, "Clear all configuration values")
	configCmd.Flags().StringVarP(&copyBankFlag, "copy-bank", "c", "", "Set the copy bank path")
	rootCmd.AddCommand(configCmd)
}
