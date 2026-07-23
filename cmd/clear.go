package cmd

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cwm/db"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	clearActiveFlag  string
	clearSavedFlag   bool
	clearHistFlag    bool
	clearFuzzyFlag   bool
	clearTagFlag     string
	clearRestoreFlag string
	clearListFlag    bool
	clearYesFlag     bool
)

type SavedCommandItem struct {
	Variable    string
	Command     string
	Tags        string
	Description string
}

type TrashedItem struct {
	ID          int
	Variable    string
	Command     string
	Tags        string
	Description string
	DeletedAt   string
}

var clearCmd = &cobra.Command{
	Use:   "clear [variable_query]",
	Short: "Clear history or saved commands, or restore deleted commands",
	Long:  `Clear saved commands, history logs, or restore deleted commands from trash (up to 100 items).`,
	Run: func(cmd *cobra.Command, args []string) {
		database, err := db.InitDB()
		if err != nil {
			fmt.Printf(color.RedString("Database error: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		// ----------------------------------------------------
		// 1. RESTORE / UNDO / LIST TRASH MODE (-r, -n)
		// ----------------------------------------------------
		if clearRestoreFlag != "" || clearListFlag {
			handleRestoreOrListTrash(database, clearRestoreFlag, clearListFlag)
			return
		}

		// ----------------------------------------------------
		// 2. CLEAR BY TAG MODE (-t)
		// ----------------------------------------------------
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
			_ = rows.Err()

			if len(varsToDelete) == 0 {
				fmt.Printf(color.RedString("No saved commands found with tag '%s'.\n"), clearTagFlag)
				similar := findSimilarTags(database, clearTagFlag)
				if len(similar) > 0 {
					fmt.Printf("Did you mean: %s?\n", strings.Join(similar, ", "))
				}
				return
			}

			if !clearYesFlag {
				if !askConfirmation(fmt.Sprintf("Are you sure you want to delete %d saved command(s) with tag '%s'?", len(varsToDelete), clearTagFlag)) {
					fmt.Println("Cancelled.")
					return
				}
			}

			// Trash commands before deleting
			if errTrash := db.TrashSavedCommands(database, varsToDelete); errTrash != nil {
				fmt.Printf(color.YellowString("Warning: Could not archive to trash: %v\n"), errTrash)
			}
			for _, v := range varsToDelete {
				_, _ = database.Exec("DELETE FROM saved_commands WHERE variable = ?", v)
			}
			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("Deleted %d saved command(s) with tag '%s' (backed up to trash).\n"), len(varsToDelete), clearTagFlag)
			return
		}

		// ----------------------------------------------------
		// 3. CLEAR ACTIVE PATH HISTORY MODE (-a)
		// ----------------------------------------------------
		if clearActiveFlag != "" {
			absPath, err := filepath.Abs(clearActiveFlag)
			if err != nil {
				fmt.Printf(color.RedString("Error resolving path: %v\n"), err)
				os.Exit(1)
			}
			absPath = filepath.ToSlash(absPath)

			if !clearYesFlag {
				if !askConfirmation(fmt.Sprintf("Are you sure you want to clear history logs for path '%s'?", absPath)) {
					fmt.Println("Cancelled.")
					return
				}
			}

			res, err := database.Exec("DELETE FROM history_logs WHERE context_dir = ?", absPath)
			if err != nil {
				fmt.Printf(color.RedString("Error clearing history: %v\n"), err)
				os.Exit(1)
			}

			rowsAffected, _ := res.RowsAffected()
			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("Deleted %d history log(s) for path: %s\n"), rowsAffected, absPath)
			return
		}

		// ----------------------------------------------------
		// 4. CLEAR ALL SAVED COMMANDS (-s)
		// ----------------------------------------------------
		if clearSavedFlag {
			var vars []string
			rows, err := database.Query("SELECT variable FROM saved_commands")
			if err == nil {
				for rows.Next() {
					var v string
					if err := rows.Scan(&v); err == nil {
						vars = append(vars, v)
					}
				}
				_ = rows.Err()
				rows.Close()
			}

			if len(vars) == 0 {
				fmt.Println(color.YellowString("No saved commands to clear."))
				return
			}

			if !clearYesFlag {
				if !askConfirmation(fmt.Sprintf("Are you sure you want to clear ALL %d saved command(s)?", len(vars))) {
					fmt.Println("Cancelled.")
					return
				}
			}

			if errTrash := db.TrashSavedCommands(database, vars); errTrash != nil {
				fmt.Printf(color.YellowString("Warning: Could not archive to trash: %v\n"), errTrash)
			}
			_, err = database.Exec("DELETE FROM saved_commands")
			if err != nil {
				fmt.Printf(color.RedString("Error clearing saved commands: %v\n"), err)
				os.Exit(1)
			}
			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("All %d saved command(s) cleared and backed up to trash.\n"), len(vars))
			return
		}

		// ----------------------------------------------------
		// 5. CLEAR ALL HISTORY LOGS (-d)
		// ----------------------------------------------------
		if clearHistFlag {
			if !clearYesFlag {
				if !askConfirmation("Are you sure you want to clear ALL database shell history logs?") {
					fmt.Println("Cancelled.")
					return
				}
			}

			_, err := database.Exec("DELETE FROM history_logs")
			if err != nil {
				fmt.Printf(color.RedString("Error clearing history: %v\n"), err)
				os.Exit(1)
			}
			_ = db.SyncToCopyBank(database)
			fmt.Println(color.GreenString("All database history logs cleared successfully."))
			return
		}

		// ----------------------------------------------------
		// 6. INTERACTIVE SELECTIVE DELETION & SEARCH MODE
		// ----------------------------------------------------
		var query string
		if len(args) > 0 {
			query = strings.Join(args, " ")
		}

		var items []SavedCommandItem
		var qErr error

		if query != "" {
			pattern := "%" + query + "%"
			if clearFuzzyFlag {
				pattern = fuzzyPattern(query)
			}
			var rows *sql.Rows
			rows, qErr = database.Query("SELECT variable, command, tags, description FROM saved_commands WHERE variable LIKE ? OR command LIKE ? ORDER BY variable ASC", pattern, pattern)
			if qErr == nil {
				for rows.Next() {
					var item SavedCommandItem
					if err := rows.Scan(&item.Variable, &item.Command, &item.Tags, &item.Description); err == nil {
						items = append(items, item)
					}
				}
				rows.Close()
			}
		} else {
			var rows *sql.Rows
			rows, qErr = database.Query("SELECT variable, command, tags, description FROM saved_commands ORDER BY variable ASC")
			if qErr == nil {
				for rows.Next() {
					var item SavedCommandItem
					if err := rows.Scan(&item.Variable, &item.Command, &item.Tags, &item.Description); err == nil {
						items = append(items, item)
					}
				}
				rows.Close()
			}
		}

		if qErr != nil {
			fmt.Printf(color.RedString("Database query error: %v\n"), qErr)
			os.Exit(1)
		}

		if len(items) == 0 {
			if query != "" {
				fmt.Printf(color.YellowString("No matching saved commands found for '%s'.\n"), query)
			} else {
				fmt.Println(color.YellowString("No saved commands found in database."))
			}
			return
		}

		// Display clean single-line table matching 'cwm get'
		fmt.Printf("\n%s\n", color.New(color.Bold, color.Underline).Sprint("Saved Commands"))
		fmt.Printf("%-5s  %-15s  %-35s  %-15s  %-20s\n", "ID", "Variable", "Command", "Tags", "Description")
		fmt.Println(strings.Repeat("-", 95))

		for i, item := range items {
			cmdPreview := item.Command
			if strings.Contains(cmdPreview, "\n") {
				cmdPreview = strings.Split(cmdPreview, "\n")[0] + "..."
			}
			if len(cmdPreview) > 35 {
				cmdPreview = cmdPreview[:32] + "..."
			}

			descPreview := item.Description
			if len(descPreview) > 20 {
				descPreview = descPreview[:17] + "..."
			}

			fmt.Printf("%-5d  %-15s  %-35s  %-15s  %-20s\n", i+1, item.Variable, cmdPreview, item.Tags, descPreview)
		}
		fmt.Println()

		fmt.Print("Enter number(s) to delete (e.g. 1, 3, 5), 'all' to delete all, or Enter to cancel: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "" {
			fmt.Println("Cancelled.")
			return
		}

		var varsToDelete []string

		if strings.ToLower(input) == "all" {
			if !clearYesFlag {
				if !askConfirmation(fmt.Sprintf("Are you sure you want to delete ALL %d command(s)?", len(items))) {
					fmt.Println("Cancelled.")
					return
				}
			}
			for _, item := range items {
				varsToDelete = append(varsToDelete, item.Variable)
			}
		} else {
			indices := parseIndices(input, len(items))
			if len(indices) == 0 {
				fmt.Println(color.RedString("Invalid selection."))
				return
			}
			for _, idx := range indices {
				varsToDelete = append(varsToDelete, items[idx-1].Variable)
			}
		}

		if len(varsToDelete) == 0 {
			fmt.Println("No items selected.")
			return
		}

		// Backup to trash first
		if errTrash := db.TrashSavedCommands(database, varsToDelete); errTrash != nil {
			fmt.Printf(color.YellowString("Warning: Could not archive to trash: %v\n"), errTrash)
		}

		// Execute deletion
		for _, v := range varsToDelete {
			_, _ = database.Exec("DELETE FROM saved_commands WHERE variable = ?", v)
		}
		_ = db.SyncToCopyBank(database)

		fmt.Printf(color.GreenString("Successfully deleted %d command(s) and moved to trash: %s\n"), len(varsToDelete), strings.Join(varsToDelete, ", "))
	},
}

// handleRestoreOrListTrash manages restoring trashed commands or listing trash contents
func handleRestoreOrListTrash(database *sql.DB, restoreVal string, listOnly bool) {
	// If specific value/name passed to restore: cwm clear -r <value>
	if restoreVal != "" && restoreVal != "__LIST__" {
		restoreSingleItem(database, restoreVal)
		return
	}

	// Fetch trash list (up to 100 items)
	rows, err := database.Query("SELECT id, variable, command, tags, description, strftime('%Y-%m-%d %H:%M:%S', deleted_at) FROM trashed_commands ORDER BY id DESC LIMIT 100")
	if err != nil {
		fmt.Printf(color.RedString("Error querying trash table: %v\n"), err)
		return
	}
	defer rows.Close()

	var trashed []TrashedItem
	for rows.Next() {
		var item TrashedItem
		var delAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Variable, &item.Command, &item.Tags, &item.Description, &delAt); err == nil {
			if delAt.Valid {
				item.DeletedAt = delAt.String
			}
			trashed = append(trashed, item)
		}
	}
	_ = rows.Err()

	if len(trashed) == 0 {
		fmt.Println(color.YellowString("Trash is empty. No deleted commands available to restore."))
		return
	}

	// Display clean single-line table matching 'cwm get'
	fmt.Printf("\n%s\n", color.New(color.Bold, color.Underline).Sprint("Trashed Commands (Last 100)"))
	fmt.Printf("%-5s  %-15s  %-35s  %-15s  %-20s\n", "ID", "Variable", "Command", "Tags", "Description")
	fmt.Println(strings.Repeat("-", 95))

	for i, item := range trashed {
		cmdPreview := item.Command
		if strings.Contains(cmdPreview, "\n") {
			cmdPreview = strings.Split(cmdPreview, "\n")[0] + "..."
		}
		if len(cmdPreview) > 35 {
			cmdPreview = cmdPreview[:32] + "..."
		}

		descPreview := item.Description
		if len(descPreview) > 20 {
			descPreview = descPreview[:17] + "..."
		}

		fmt.Printf("%-5d  %-15s  %-35s  %-15s  %-20s\n", i+1, item.Variable, cmdPreview, item.Tags, descPreview)
	}
	fmt.Println()

	// If -n flag passed without -r, just display list
	if listOnly && restoreVal == "" {
		return
	}

	// Prompt user to select items to restore
	fmt.Print("Enter number(s) to restore (e.g. 1, 3, 5), 'all' to restore all, or Enter to cancel: ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		fmt.Println("Cancelled.")
		return
	}

	var selected []TrashedItem
	if strings.ToLower(input) == "all" {
		selected = trashed
	} else {
		indices := parseIndices(input, len(trashed))
		if len(indices) == 0 {
			fmt.Println(color.RedString("Invalid selection."))
			return
		}
		for _, idx := range indices {
			selected = append(selected, trashed[idx-1])
		}
	}

	restoredCount := 0
	for _, item := range selected {
		if restoreItemWithPrompt(database, item) {
			restoredCount++
		}
	}

	if restoredCount > 0 {
		_ = db.SyncToCopyBank(database)
	}
	fmt.Printf(color.GreenString("Restore process completed: %d command(s) processed.\n"), restoredCount)
}

// restoreSingleItem restores a specific item by variable name
func restoreSingleItem(database *sql.DB, targetVar string) {
	var item TrashedItem
	err := database.QueryRow("SELECT id, variable, command, tags, description FROM trashed_commands WHERE variable = ? ORDER BY id DESC LIMIT 1", targetVar).
		Scan(&item.ID, &item.Variable, &item.Command, &item.Tags, &item.Description)

	if err != nil {
		fmt.Printf(color.YellowString("No trashed command found matching variable '%s'.\n"), targetVar)
		return
	}

	if restoreItemWithPrompt(database, item) {
		_ = db.SyncToCopyBank(database)
	}
}

// restoreItemWithPrompt handles conflict resolution with interactive prompt
func restoreItemWithPrompt(database *sql.DB, item TrashedItem) bool {
	var activeCmd, activeDesc string
	err := database.QueryRow("SELECT command, description FROM saved_commands WHERE variable = ?", item.Variable).Scan(&activeCmd, &activeDesc)

	if err == sql.ErrNoRows {
		// No conflict: direct restore
		_, errIns := database.Exec("INSERT INTO saved_commands (variable, command, tags, description) VALUES (?, ?, ?, ?)",
			item.Variable, item.Command, item.Tags, item.Description)
		if errIns != nil {
			fmt.Printf(color.RedString("Error restoring '%s': %v\n"), item.Variable, errIns)
			return false
		}
		_, _ = database.Exec("DELETE FROM trashed_commands WHERE id = ?", item.ID)
		fmt.Printf(color.GreenString("Restored command: %s\n"), item.Variable)
		return true
	}

	if err != nil {
		fmt.Printf(color.RedString("Database error checking conflict for '%s': %v\n"), item.Variable, err)
		return false
	}

	// Conflict detected! Prompt options: Overwrite, Rename, Skip
	fmt.Println()
	fmt.Printf(color.YellowString("Conflict detected for variable '%s':\n"), item.Variable)
	fmt.Printf("  Active Command:   %s\n", activeCmd)
	fmt.Printf("  Trashed Command:  %s\n", item.Command)
	fmt.Println("Actions:")
	fmt.Println("  1) Overwrite active command with trashed command")
	fmt.Println("  2) Rename variable for restored command")
	fmt.Println("  3) Skip / Keep active command")
	fmt.Print("Select (1/2/3) [default: 3]: ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "1":
		// Overwrite active command
		_, errUpd := database.Exec("UPDATE saved_commands SET command = ?, tags = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE variable = ?",
			item.Command, item.Tags, item.Description, item.Variable)
		if errUpd != nil {
			fmt.Printf(color.RedString("Error updating active command '%s': %v\n"), item.Variable, errUpd)
			return false
		}
		_, _ = database.Exec("DELETE FROM trashed_commands WHERE id = ?", item.ID)
		fmt.Printf(color.GreenString("Replaced active command '%s' with trashed command.\n"), item.Variable)
		return true

	case "2":
		// Rename restored variable
		fmt.Printf("Enter new variable name for restored command [%s_restored]: ", item.Variable)
		newVar, _ := reader.ReadString('\n')
		newVar = strings.TrimSpace(newVar)
		if newVar == "" {
			newVar = item.Variable + "_restored"
		}

		var newExists int
		database.QueryRow("SELECT COUNT(*) FROM saved_commands WHERE variable = ?", newVar).Scan(&newExists)
		if newExists > 0 {
			fmt.Printf(color.RedString("Variable '%s' also exists. Skipping restore.\n"), newVar)
			return false
		}

		_, errIns := database.Exec("INSERT INTO saved_commands (variable, command, tags, description) VALUES (?, ?, ?, ?)",
			newVar, item.Command, item.Tags, item.Description)
		if errIns != nil {
			fmt.Printf(color.RedString("Error inserting restored command '%s': %v\n"), newVar, errIns)
			return false
		}
		_, _ = database.Exec("DELETE FROM trashed_commands WHERE id = ?", item.ID)
		fmt.Printf(color.GreenString("Restored command under new variable name: %s\n"), newVar)
		return true

	default:
		// Skip
		fmt.Printf(color.YellowString("Skipped restore for '%s'. Active command preserved.\n"), item.Variable)
		return false
	}
}

// askConfirmation prompts user for y/N confirmation
func askConfirmation(message string) bool {
	fmt.Printf("%s (y/N): ", message)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(answer)
	return strings.ToLower(answer) == "y" || strings.ToLower(answer) == "yes"
}

// parseIndices parses input string like "1, 3, 5" into 1-based unique valid indices
func parseIndices(input string, max int) []int {
	var indices []int
	seen := make(map[int]bool)
	cleaned := strings.ReplaceAll(input, ",", " ")
	fields := strings.Fields(cleaned)
	for _, f := range fields {
		var num int
		if _, err := fmt.Sscanf(f, "%d", &num); err == nil {
			if num >= 1 && num <= max && !seen[num] {
				seen[num] = true
				indices = append(indices, num)
			}
		}
	}
	return indices
}

func init() {
	clearCmd.Flags().StringVarP(&clearActiveFlag, "active", "a", "", "Clear watch history matching path")
	clearCmd.Flags().BoolVarP(&clearSavedFlag, "saved", "s", false, "Clear all saved commands")
	clearCmd.Flags().BoolVarP(&clearHistFlag, "history", "d", false, "Clear all database shell history logs")
	clearCmd.Flags().BoolVarP(&clearFuzzyFlag, "fuzzy", "f", false, "Fuzzy clear saved commands matching query")
	clearCmd.Flags().StringVarP(&clearTagFlag, "tag", "t", "", "Clear saved commands matching tag exactly")
	clearCmd.Flags().StringVarP(&clearRestoreFlag, "restore", "r", "", "Restore trashed commands (pass variable name, or run without args to list & select)")
	clearCmd.Flags().Lookup("restore").NoOptDefVal = "__LIST__"
	clearCmd.Flags().BoolVarP(&clearListFlag, "list", "n", false, "List trashed commands")
	clearCmd.Flags().BoolVarP(&clearYesFlag, "yes", "y", false, "Skip confirmation prompts")

	rootCmd.AddCommand(clearCmd)
}
