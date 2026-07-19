package cmd

import (
	"bufio"
	"cwm/db"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	clearActiveFlag string
	clearSavedFlag  bool
	clearHistFlag   bool
	clearFuzzyFlag  bool
	clearTagFlag    string
)

var clearCmd = &cobra.Command{
	Use:   "clear [variable_query]",
	Short: "Clear history or saved commands",
	Long:  `Clear database saved commands, unified history logs, or path-specific watch histories.`,
	Run: func(cmd *cobra.Command, args []string) {
		database, err := db.InitDB()
		if err != nil {
			fmt.Printf(color.RedString("Database error: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		// 1. Clear by Tag Mode (No fuzzy tag match, exact match. Shows similar if not found)
		if clearTagFlag != "" {
			rows, err := database.Query("SELECT variable, tags FROM saved_commands")
			if err != nil {
				fmt.Printf(color.RedString("Error querying database: %v\n"), err)
				os.Exit(1)
			}
			defer rows.Close()

			var varsToDelete []string
			targetTag := strings.ToLower(strings.TrimSpace(clearTagFlag))

			for rows.Next() {
				var variable, tagStr string
				if err := rows.Scan(&variable, &tagStr); err == nil {
					parts := strings.Split(tagStr, ",")
					for _, p := range parts {
						if strings.ToLower(strings.TrimSpace(p)) == targetTag {
							varsToDelete = append(varsToDelete, variable)
							break
						}
					}
				}
			}
			if err = rows.Err(); err != nil {
				fmt.Printf(color.RedString("Error reading database rows: %v\n"), err)
				os.Exit(1)
			}

			if len(varsToDelete) == 0 {
				fmt.Printf(color.RedString("No tag '%s' found.\n"), clearTagFlag)
				similar := findSimilarTags(database, clearTagFlag)
				if len(similar) > 0 {
					fmt.Printf("Did you mean: %s?\n", strings.Join(similar, ", "))
				}
				return
			}

			for _, v := range varsToDelete {
				_, _ = database.Exec("DELETE FROM saved_commands WHERE variable = ?", v)
			}
			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("Deleted %d saved commands with tag: ")+"%s\n", len(varsToDelete), clearTagFlag)
			return
		}

		// 2. Clear Active Path History Mode
		if clearActiveFlag != "" {
			absPath, err := filepath.Abs(clearActiveFlag)
			if err != nil {
				fmt.Printf(color.RedString("Error resolving path: %v\n"), err)
				os.Exit(1)
			}
			absPath = filepath.ToSlash(absPath)

			res, err := database.Exec("DELETE FROM history_logs WHERE context_dir = ?", absPath)
			if err != nil {
				fmt.Printf(color.RedString("Error clearing history: %v\n"), err)
				os.Exit(1)
			}

			rowsAffected, _ := res.RowsAffected()
			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("Deleted %d history logs for path: ")+"%s\n", rowsAffected, absPath)
			return
		}

		// 3. Clear Saved Commands by Name/Fuzzy Query
		if len(args) > 0 {
			search := strings.Join(args, " ")
			if clearFuzzyFlag {
				pattern := fuzzyPattern(search)
				rows, err := database.Query("SELECT variable FROM saved_commands WHERE variable LIKE ? OR command LIKE ?", pattern, pattern)
				if err != nil {
					fmt.Printf(color.RedString("Error querying database: %v\n"), err)
					os.Exit(1)
				}
				defer rows.Close()

				var varsToDelete []string
				for rows.Next() {
					var v string
					if err := rows.Scan(&v); err == nil {
						varsToDelete = append(varsToDelete, v)
					}
				}
				if err = rows.Err(); err != nil {
					fmt.Printf(color.RedString("Error scanning database rows: %v\n"), err)
					os.Exit(1)
				}

				if len(varsToDelete) == 0 {
					fmt.Println(color.YellowString("No matching saved commands found to clear."))
					return
				}

				for _, v := range varsToDelete {
					_, _ = database.Exec("DELETE FROM saved_commands WHERE variable = ?", v)
				}
				_ = db.SyncToCopyBank(database)
				fmt.Printf(color.GreenString("Deleted %d saved commands: ")+"%s\n", len(varsToDelete), strings.Join(varsToDelete, ", "))
			} else {
				res, err := database.Exec("DELETE FROM saved_commands WHERE variable = ?", search)
				if err != nil {
					fmt.Printf(color.RedString("Error deleting command: %v\n"), err)
					os.Exit(1)
				}
				rowsAffected, _ := res.RowsAffected()
				if rowsAffected == 0 {
					fmt.Printf(color.YellowString("No saved command found with alias '%s'.\n"), search)
					return
				}
				_ = db.SyncToCopyBank(database)
				fmt.Printf(color.GreenString("Deleted saved command: ")+"%s\n", search)
			}
			return
		}

		// 4. Clear Saved Commands only (All)
		if clearSavedFlag {
			_, err := database.Exec("DELETE FROM saved_commands")
			if err != nil {
				fmt.Printf(color.RedString("Error clearing saved commands: %v\n"), err)
				os.Exit(1)
			}
			_ = db.SyncToCopyBank(database)
			fmt.Println(color.GreenString("All saved commands cleared successfully."))
			return
		}

		// 5. Clear History Logs only (All)
		if clearHistFlag {
			_, err := database.Exec("DELETE FROM history_logs")
			if err != nil {
				fmt.Printf(color.RedString("Error clearing history: %v\n"), err)
				os.Exit(1)
			}
			_ = db.SyncToCopyBank(database)
			fmt.Println(color.GreenString("All database history logs cleared successfully."))
			return
		}

		// 6. Default Mode: List files and ask to clear everything
		dbPath, _ := db.GetDBPath()
		fmt.Println()
		fmt.Println(color.New(color.Bold, color.FgCyan).Sprint("CWM Storage Info"))
		fmt.Printf("  Database File: %s\n", dbPath)
		if fileInfo, err := os.Stat(dbPath); err == nil {
			fmt.Printf("  File Size:     %.2f KB\n", float64(fileInfo.Size())/1024.0)
		}

		var savedCount, histCount int
		database.QueryRow("SELECT COUNT(*) FROM saved_commands").Scan(&savedCount)
		database.QueryRow("SELECT COUNT(*) FROM history_logs").Scan(&histCount)
		fmt.Printf("  Saved Commands: %d\n", savedCount)
		fmt.Printf("  History Logs:   %d\n", histCount)
		fmt.Println()

		fmt.Print("Do you want to clear all saved commands and history? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		confirm, _ := reader.ReadString('\n')
		confirm = strings.ToLower(strings.TrimSpace(confirm))

		if confirm == "y" || confirm == "yes" {
			_, err1 := database.Exec("DELETE FROM saved_commands")
			_, err2 := database.Exec("DELETE FROM history_logs")
			if err1 != nil || err2 != nil {
				fmt.Println(color.RedString("Error clearing storage."))
			} else {
				_ = db.SyncToCopyBank(database)
				fmt.Println(color.GreenString("All saved commands and history cleared successfully."))
			}
		} else {
			fmt.Println("Cancelled.")
		}
	},
}

func init() {
	clearCmd.Flags().StringVarP(&clearActiveFlag, "active", "a", "", "Clear watch history matching path")
	clearCmd.Flags().BoolVarP(&clearSavedFlag, "saved", "s", false, "Clear all saved commands")
	clearCmd.Flags().BoolVarP(&clearHistFlag, "history", "d", false, "Clear all database shell history logs")
	clearCmd.Flags().BoolVarP(&clearFuzzyFlag, "fuzzy", "f", false, "Fuzzy clear saved commands matching query")
	clearCmd.Flags().StringVarP(&clearTagFlag, "tag", "t", "", "Clear saved commands matching tag exactly")
	rootCmd.AddCommand(clearCmd)
}
