package cmd

import (
	"cwm/db"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	tidyMaxLines int
	tidyMaxChars int
	tidyMaxWords int
	tidyYesFlag  bool
)

var tidyCmd = &cobra.Command{
	Use:   "tidy [history|watch]",
	Short: "Tidy shell history file or watch database logs",
	Long:  `Deduplicate and clean shell history files on disk or recorded watch logs in database with path-aware context matching.`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			runTidyHistory()
			fmt.Println()
			runTidyWatch()
			return
		}
		_ = cmd.Help()
	},
}

var tidyHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Tidy active shell history file on disk",
	Run: func(cmd *cobra.Command, args []string) {
		runTidyHistory()
	},
}

var tidyWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Tidy watch history database logs (path-aware deduplication)",
	Run: func(cmd *cobra.Command, args []string) {
		runTidyWatch()
	},
}

func runTidyHistory() {
	database, err := db.InitDB()
	if err != nil {
		fmt.Printf(color.RedString("Database error: %v\n"), err)
		os.Exit(1)
	}
	defer database.Close()

	histPath := getHistoryFilePath(database)
	if histPath == "" {
		fmt.Println(color.YellowString("No active shell history file detected to tidy."))
		return
	}

	fileLines, err := readLines(histPath)
	if err != nil {
		fmt.Printf(color.RedString("Error reading history file: %v\n"), err)
		return
	}

	if len(fileLines) == 0 {
		fmt.Println(color.YellowString("History file is already empty."))
		return
	}

	var tidied []string
	seen := make(map[string]bool)
	dupCount := 0
	charBloatCount := 0
	wordBloatCount := 0

	for _, line := range fileLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// 1. Check duplicates
		if seen[trimmed] {
			dupCount++
			continue
		}

		// 2. Check max characters limit (-c)
		if tidyMaxChars > 0 && len(trimmed) > tidyMaxChars {
			charBloatCount++
			continue
		}

		// 3. Check max words limit (-w)
		if tidyMaxWords > 0 && len(strings.Fields(trimmed)) > tidyMaxWords {
			wordBloatCount++
			continue
		}

		seen[trimmed] = true
		tidied = append(tidied, line)
	}

	fmt.Printf("\n%s\n", color.New(color.Bold, color.Underline).Sprint("Shell History Analysis (File Disk)"))
	fmt.Printf("  Target File:         %s\n", color.CyanString(histPath))
	fmt.Printf("  • Total Raw Lines:   %d\n", len(fileLines))
	fmt.Printf("  • Duplicate Lines:   %d\n", dupCount)
	if tidyMaxChars > 0 {
		fmt.Printf("  • Oversized (>%d ch): %d\n", tidyMaxChars, charBloatCount)
	} else {
		fmt.Printf("  • Oversized Chars:   Disabled (-c 0)\n")
	}
	if tidyMaxWords > 0 {
		fmt.Printf("  • Oversized (>%d w):  %d\n", tidyMaxWords, wordBloatCount)
	} else {
		fmt.Printf("  • Oversized Words:   Disabled (-w 0)\n")
	}
	fmt.Printf("  • Optimized Result:  %d -> %d lines\n\n", len(fileLines), len(tidied))

	if !tidyYesFlag {
		if !askConfirmation("Apply shell history tidying now?") {
			fmt.Println("Cancelled.")
			return
		}
	}

	outputContent := strings.Join(tidied, "\n") + "\n"
	if errWrite := os.WriteFile(histPath, []byte(outputContent), 0644); errWrite != nil {
		fmt.Printf(color.RedString("Error updating history file: %v\n"), errWrite)
		return
	}

	fmt.Printf("%s\n", color.GreenString("Successfully tidied shell history file (%d -> %d lines).", len(fileLines), len(tidied)))
}

func runTidyWatch() {
	database, err := db.InitDB()
	if err != nil {
		fmt.Printf(color.RedString("Database error: %v\n"), err)
		os.Exit(1)
	}
	defer database.Close()

	rows, err := database.Query("SELECT rowid, command, COALESCE(context_dir, '') FROM history_logs ORDER BY rowid ASC")
	if err != nil {
		fmt.Printf(color.RedString("Error querying watch history database: %v\n"), err)
		return
	}
	defer rows.Close()

	type logEntry struct {
		id      int64
		cmd     string
		context string
	}

	var allEntries []logEntry
	for rows.Next() {
		var entry logEntry
		if errScan := rows.Scan(&entry.id, &entry.cmd, &entry.context); errScan == nil {
			allEntries = append(allEntries, entry)
		}
	}
	_ = rows.Err()

	if len(allEntries) == 0 {
		fmt.Println(color.YellowString("Watch database history logs are empty."))
		return
	}

	seenKey := make(map[string]bool)
	var deleteIDs []int64

	for _, entry := range allEntries {
		trimmedCmd := strings.TrimSpace(entry.cmd)
		if trimmedCmd == "" {
			deleteIDs = append(deleteIDs, entry.id)
			continue
		}

		// PATH-AWARE DEDUPLICATION KEY: context_dir + "|||" + command
		key := entry.context + "|||" + trimmedCmd

		if seenKey[key] {
			deleteIDs = append(deleteIDs, entry.id)
			continue
		}

		seenKey[key] = true
	}

	fmt.Printf("\n%s\n", color.New(color.Bold, color.Underline).Sprint("Watch History Analysis (SQLite Database)"))
	fmt.Printf("  • Total Log Entries:            %d\n", len(allEntries))
	fmt.Printf("  • Path-Aware Duplicates Found:  %d\n", len(deleteIDs))
	fmt.Printf("  • Optimized Result:             %d -> %d database entries\n\n", len(allEntries), len(allEntries)-len(deleteIDs))

	if len(deleteIDs) == 0 {
		fmt.Println(color.GreenString("Watch history database is already clean (0 path-aware duplicates found)."))
		return
	}

	if !tidyYesFlag {
		if !askConfirmation("Apply path-aware watch history tidying now?") {
			fmt.Println("Cancelled.")
			return
		}
	}

	// Delete duplicate IDs in batches
	tx, errTx := database.Begin()
	if errTx == nil {
		stmt, errStmt := tx.Prepare("DELETE FROM history_logs WHERE rowid = ?")
		if errStmt == nil {
			for _, id := range deleteIDs {
				_, _ = stmt.Exec(id)
			}
			stmt.Close()
		}
		_ = tx.Commit()
	}

	_ = db.SyncToCopyBank(database)
	fmt.Printf("%s\n", color.GreenString("Successfully tidied watch database logs (removed %d path-aware duplicates).", len(deleteIDs)))
}

func init() {
	tidyCmd.PersistentFlags().IntVarP(&tidyMaxLines, "max-lines", "n", 10, "Remove multiline commands exceeding N lines")
	tidyCmd.PersistentFlags().IntVarP(&tidyMaxChars, "max-chars", "c", 200, "Remove command lines exceeding N characters (0 to disable)")
	tidyCmd.PersistentFlags().IntVarP(&tidyMaxWords, "max-words", "w", 50, "Remove command lines exceeding N words (0 to disable)")
	tidyCmd.PersistentFlags().BoolVarP(&tidyYesFlag, "yes", "y", false, "Skip confirmation prompt")

	tidyCmd.AddCommand(tidyHistoryCmd)
	tidyCmd.AddCommand(tidyWatchCmd)

	rootCmd.AddCommand(tidyCmd)
}
