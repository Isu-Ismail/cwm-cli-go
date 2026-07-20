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
	clearConfigFlag       bool
	copyBankFlag          string
	changeHistoryFileFlag string
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration settings",
	Long:  `Configure copy bank path, change shell history file location, or clear all configurations.`,
	Run: func(cmd *cobra.Command, args []string) {
		database, err := db.InitDB()
		if err != nil {
			fmt.Printf(color.RedString("Database error: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		// 1. Set copy bank path
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

		// 2. Change shell history file path (history_file)
		if cmd.Flags().Changed("change-history-file") || cmd.Flags().Changed("change-history-dir") {
			val := strings.TrimSpace(changeHistoryFileFlag)
			if val == "" || strings.ToLower(val) == "clear" || strings.ToLower(val) == "none" {
				err = db.SetConfigValue(database, "history_file", "")
				fmt.Println(color.GreenString("History file path cleared."))
			} else {
				absVal, absErr := filepath.Abs(val)
				if absErr != nil {
					fmt.Printf(color.RedString("Error: Invalid path: %v\n"), absErr)
					os.Exit(1)
				}
				val = filepath.ToSlash(absVal)

				// 1. Must be a file
				info, statErr := os.Stat(val)
				if statErr != nil {
					fmt.Printf(color.RedString("Error: File does not exist at path: %s\n"), val)
					os.Exit(1)
				}
				if info.IsDir() {
					fmt.Println(color.RedString("Error: Path must point to a file, not a directory."))
					os.Exit(1)
				}

				// 2. Filename must contain "history"
				filename := strings.ToLower(filepath.Base(val))
				if !strings.Contains(filename, "history") {
					fmt.Println(color.RedString("Error: Custom history file must contain the word 'history' in its name."))
					os.Exit(1)
				}

				// 3. Scan words limit to avoid hanging
				if errVal := validateHistoryFile(val); errVal != nil {
					fmt.Printf("%s", color.RedString("Error: Invalid history file. Any single line cannot exceed 100 words (detected block of continuous text).\n"))
					os.Exit(1)
				}

				err = db.SetConfigValue(database, "history_file", val)
				fmt.Printf(color.GreenString("Set history file path: ")+"%s\n", val)
			}

			if err != nil {
				fmt.Printf(color.RedString("Error writing configuration: %v\n"), err)
				os.Exit(1)
			}
			return
		}

		// 3. Clear all configurations
		if clearConfigFlag {
			if err := db.ClearConfig(database); err != nil {
				fmt.Printf(color.RedString("Error clearing config: %v\n"), err)
				os.Exit(1)
			}
			fmt.Println(color.GreenString("All configurations cleared."))
			return
		}

		// 4. Default: Show Current Configurations
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
	},
}

func init() {
	configCmd.Flags().BoolVar(&clearConfigFlag, "clear", false, "Clear all configuration values")
	configCmd.Flags().StringVarP(&copyBankFlag, "copy-bank", "c", "", "Set the copy bank path")
	configCmd.Flags().StringVar(&changeHistoryFileFlag, "change-history-file", "", "Set custom history file path")
	configCmd.Flags().StringVar(&changeHistoryFileFlag, "change-history-dir", "", "Set custom history file path (alias)")
	rootCmd.AddCommand(configCmd)
}
