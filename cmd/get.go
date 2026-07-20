package cmd

import (
	"bufio"
	"cwm/db"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	getTagFlag string
	showFlag   bool
	listFlag   bool
	copyFlag   bool
	histFlag   bool
	activeFlag bool
	countFlag  int
	fuzzyFlag  bool
)

var getCmd = &cobra.Command{
	Use:   "get [variable_name_or_id]",
	Short: "Retrieve and copy saved commands or history",
	Long:  `Retrieve a saved command by its alias or ID and copy it to clipboard. If multiple or no arguments are provided, lists them in an interactive menu.`,
	Run: func(cmd *cobra.Command, args []string) {
		// 1. Connect to Database (check copy bank redirect)
		database, err := db.GetDBConn(copyFlag)
		if err != nil {
			fmt.Printf(color.RedString("Error connecting to database: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		var search string
		if len(args) > 0 {
			search = strings.Join(args, " ")
		}

		// 2. History Modes
		if histFlag || activeFlag {
			handleHistoryLookup(database, search)
			return
		}

		// 3. Single Fetch Mode (Exact match, only if not fuzzy)
		if len(args) == 1 && !listFlag && !fuzzyFlag {
			handleSingleFetch(database, args[0])
			return
		}

		// 4. Interactive List Mode (lists saved commands, optionally fuzzy filtered)
		handleSavedListLookup(database, search)
	},
}

func handleSingleFetch(database *sql.DB, search string) {
	var commandVal string
	// Query by variable name directly
	err := database.QueryRow("SELECT command FROM saved_commands WHERE variable = ?", search).Scan(&commandVal)

	if err == sql.ErrNoRows {
		fmt.Printf(color.RedString("Error: Command '%s' not found.\n"), search)
		os.Exit(1)
	} else if err != nil {
		fmt.Printf(color.RedString("Error searching command: %v\n"), err)
		os.Exit(1)
	}

	if showFlag {
		fmt.Println(commandVal)
	} else {
		if err := clipboard.WriteAll(commandVal); err != nil {
			fmt.Printf(color.YellowString("Warning: Failed to write to clipboard: %v\n"), err)
			fmt.Println(commandVal)
		} else {
			fmt.Printf(color.GreenString("Copied to clipboard! ") + "%s\n", commandVal)
		}
	}
}

func fuzzyPattern(search string) string {
	var builder strings.Builder
	builder.WriteRune('%')
	for _, r := range search {
		builder.WriteRune(r)
		builder.WriteRune('%')
	}
	return builder.String()
}

func matchesFuzzy(text, pattern string) bool {
	text = strings.ToLower(text)
	patternIdx := 0
	for _, char := range text {
		if patternIdx < len(pattern) && char == rune(pattern[patternIdx]) {
			patternIdx++
		}
	}
	return patternIdx == len(pattern)
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func isCwmCall(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "cwm ") || strings.HasPrefix(s, "cwm.exe ") || s == "cwm" || s == "cwm.exe"
}

func validateHistoryFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineCount := 0
	for scanner.Scan() {
		lineCount++
		if lineCount > 5000 {
			break
		}
		line := scanner.Text()
		words := strings.Fields(line)
		if len(words) > 100 {
			return fmt.Errorf("line exceeds maximum of 100 words")
		}
	}
	return scanner.Err()
}

func getDefaultHistoryFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	shellType := detectShell()
	if shellType == "zsh" {
		return filepath.Join(home, ".zsh_history")
	}
	if shellType == "bash" {
		return filepath.Join(home, ".bash_history")
	}
	if shellType == "pwsh" || shellType == "powershell" {
		appData := os.Getenv("APPDATA")
		if appData != "" {
			return filepath.Join(appData, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
		}
	}

	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData != "" {
			return filepath.Join(appData, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
		}
	}
	return filepath.Join(home, ".bash_history")
}

func getHistoryFilePath(database *sql.DB) string {
	path, _ := db.GetConfigValue(database, "history_file")
	if path != "" {
		// 1. Must be a file
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			if database != nil {
				_ = db.SetConfigValue(database, "history_file", "")
			}
			return getDefaultHistoryFilePath()
		}

		// 2. Filename must contain "history"
		filename := strings.ToLower(filepath.Base(path))
		if !strings.Contains(filename, "history") {
			if database != nil {
				_ = db.SetConfigValue(database, "history_file", "")
			}
			return getDefaultHistoryFilePath()
		}

		// 3. Scan words limit to avoid hanging
		if errVal := validateHistoryFile(path); errVal != nil {
			if database != nil {
				_ = db.SetConfigValue(database, "history_file", "")
			}
			return getDefaultHistoryFilePath()
		}

		return path
	}

	return getDefaultHistoryFilePath()
}

func handleSavedListLookup(database *sql.DB, search string) {
	var rows *sql.Rows
	var err error

	if search != "" && fuzzyFlag {
		query := "SELECT variable, command, tags FROM saved_commands WHERE variable LIKE ? OR command LIKE ? ORDER BY created_at ASC"
		pattern := fuzzyPattern(search)
		rows, err = database.Query(query, pattern, pattern)
	} else {
		query := "SELECT variable, command, tags FROM saved_commands ORDER BY created_at ASC"
		rows, err = database.Query(query)
	}

	if err != nil {
		fmt.Printf(color.RedString("Error querying database: %v\n"), err)
		os.Exit(1)
	}
	defer rows.Close()

	type cmdItem struct {
		varName string
		cmdStr  string
		tags    string
	}

	var items []cmdItem
	tagMatchedAny := false
	for rows.Next() {
		var item cmdItem
		if err := rows.Scan(&item.varName, &item.cmdStr, &item.tags); err != nil {
			continue
		}

		if getTagFlag != "" {
			tagMatched := false
			parts := strings.Split(item.tags, ",")
			target := strings.ToLower(strings.TrimSpace(getTagFlag))
			for _, p := range parts {
				if strings.ToLower(strings.TrimSpace(p)) == target {
					tagMatched = true
					tagMatchedAny = true
					break
				}
			}
			if !tagMatched {
				continue
			}
		}

		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		fmt.Printf(color.RedString("Error scanning database rows: %v\n"), err)
		os.Exit(1)
	}

	if getTagFlag != "" && !tagMatchedAny {
		fmt.Printf(color.RedString("No tag '%s' found.\n"), getTagFlag)
		similar := findSimilarTags(database, getTagFlag)
		if len(similar) > 0 {
			fmt.Printf("Did you mean: %s?\n", strings.Join(similar, ", "))
		}
		return
	}

	if len(items) == 0 {
		fmt.Println(color.YellowString("No matching saved commands found."))
		return
	}

	// Apply count limit (e.g. show latest N commands)
	limit := countFlag
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}

	fmt.Printf("\n%s\n", color.New(color.Bold, color.Underline).Sprint("Saved Commands"))
	fmt.Printf("%-5s  %-15s  %-40s  %-20s\n", "ID", "Variable", "Command", "Tags")
	fmt.Println(strings.Repeat("-", 85))

	displayMap := make(map[string]string)
	for i, item := range items {
		displayId := i + 1
		idStr := strconv.Itoa(displayId)
		displayMap[idStr] = item.cmdStr

		cmdPreview := item.cmdStr
		if len(cmdPreview) > 40 {
			cmdPreview = cmdPreview[:37] + "..."
		}
		fmt.Printf("%-5d  %-15s  %-40s  %-20s\n", displayId, item.varName, cmdPreview, item.tags)
	}
	fmt.Println()

	if listFlag {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Copy (ID): ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" {
		return
	}

	if cmdStr, ok := displayMap[choice]; ok {
		if err := clipboard.WriteAll(cmdStr); err != nil {
			fmt.Printf(color.YellowString("Warning: Failed to copy to clipboard: %v\n"), err)
			fmt.Println(cmdStr)
		} else {
			fmt.Printf(color.GreenString("Copied command #%s -> ")+"%s\n", choice, cmdStr)
		}
	} else {
		fmt.Println(color.RedString("Invalid ID selected."))
	}
}

func handleHistoryLookup(database *sql.DB, search string) {
	var lines []string

	if activeFlag {
		rows, err := database.Query("SELECT command FROM history_logs WHERE context_dir = ? ORDER BY logged_at DESC LIMIT 5000", detectCwd())
		if err != nil {
			fmt.Printf(color.RedString("Error querying database history: %v\n"), err)
			os.Exit(1)
		}
		defer rows.Close()
		for rows.Next() {
			var cmdVal string
			if err := rows.Scan(&cmdVal); err == nil {
				lines = append(lines, cmdVal)
			}
		}
		if err = rows.Err(); err != nil {
			fmt.Printf(color.RedString("Error scanning history rows: %v\n"), err)
			os.Exit(1)
		}
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	} else {
		histPath := getHistoryFilePath(database)
		if histPath == "" {
			fmt.Println(color.YellowString("System history file not detected."))
			return
		}

		fileLines, err := readLines(histPath)
		if err != nil {
			fmt.Printf(color.YellowString("Warning: Could not read history file: %v\n"), err)
			return
		}

		for _, line := range fileLines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, ": ") && strings.Contains(line, ";") {
				parts := strings.SplitN(line, ";", 2)
				line = strings.TrimSpace(parts[1])
			}
			if isCwmCall(line) {
				continue
			}
			lines = append(lines, line)
		}
	}

	var filteredLines []string
	if search != "" && fuzzyFlag {
		pattern := strings.ToLower(search)
		for _, line := range lines {
			if matchesFuzzy(line, pattern) {
				filteredLines = append(filteredLines, line)
			}
		}
	} else {
		filteredLines = lines
	}

	var uniqueLines []string
	seen := make(map[string]bool)
	for i := len(filteredLines) - 1; i >= 0; i-- {
		cmdStr := filteredLines[i]
		if !seen[cmdStr] {
			uniqueLines = append(uniqueLines, cmdStr)
			seen[cmdStr] = true
		}
	}
	for i, j := 0, len(uniqueLines)-1; i < j; i, j = i+1, j-1 {
		uniqueLines[i], uniqueLines[j] = uniqueLines[j], uniqueLines[i]
	}

	if len(uniqueLines) == 0 {
		fmt.Println(color.YellowString("No history logs found matching criteria."))
		return
	}

	finalLines := uniqueLines
	limit := countFlag
	if limit > 0 && len(uniqueLines) > limit {
		finalLines = uniqueLines[len(uniqueLines)-limit:]
	}

	title := "System Shell History"
	if activeFlag {
		title = "Local Project History"
	}
	fmt.Printf("\n%s (%d/%d)\n", color.New(color.Bold, color.Underline).Sprint(title), len(finalLines), len(uniqueLines))
	fmt.Printf("%-5s  %-60s\n", "ID", "Command")
	fmt.Println(strings.Repeat("-", 70))

	displayMap := make(map[string]string)
	for i, line := range finalLines {
		displayNum := strconv.Itoa(i + 1)
		displayMap[displayNum] = line
		fmt.Printf("%-5s  %-60s\n", displayNum, line)
	}
	fmt.Println()

	if listFlag {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Copy (ID): ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" {
		return
	}

	if cmdStr, ok := displayMap[choice]; ok {
		if err := clipboard.WriteAll(cmdStr); err != nil {
			fmt.Printf(color.YellowString("Warning: Failed to copy to clipboard: %v\n"), err)
			fmt.Println(cmdStr)
		} else {
			fmt.Printf(color.GreenString("Copied command #%s -> ")+"%s\n", choice, cmdStr)
		}
	} else {
		fmt.Println(color.RedString("Invalid ID selected."))
	}
}

func detectCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return cwd
}

func init() {
	// Define the help flag manually without shorthand "h" to prevent Cobra from defining one with "h"
	getCmd.Flags().Bool("help", false, "help for get")

	getCmd.Flags().StringVarP(&getTagFlag, "tag", "t", "", "Filter saved commands by tag")
	getCmd.Flags().BoolVarP(&showFlag, "show", "s", false, "Show command value without copying")
	getCmd.Flags().BoolVarP(&listFlag, "list", "l", false, "List commands without prompt")
	getCmd.Flags().BoolVarP(&copyFlag, "copy", "c", false, "Read database from copy bank")
	getCmd.Flags().BoolVarP(&histFlag, "hist", "h", false, "Lookup shell history logs")
	getCmd.Flags().BoolVarP(&activeFlag, "active", "a", false, "Lookup local project shell history logs")
	getCmd.Flags().IntVarP(&countFlag, "count", "n", 10, "Limit number of history logs loaded")
	getCmd.Flags().BoolVarP(&fuzzyFlag, "fuzzy", "f", false, "Fuzzy search saved commands or history")
	rootCmd.AddCommand(getCmd)
}

func findSimilarTags(database *sql.DB, targetTag string) []string {
	rows, err := database.Query("SELECT tags FROM saved_commands")
	if err != nil {
		return nil
	}
	defer rows.Close()

	tagSet := make(map[string]bool)
	for rows.Next() {
		var tagStr string
		if err := rows.Scan(&tagStr); err == nil {
			parts := strings.Split(tagStr, ",")
			for _, p := range parts {
				p = strings.ToLower(strings.TrimSpace(p))
				if p != "" {
					tagSet[p] = true
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil
	}

	var similar []string
	targetLower := strings.ToLower(targetTag)
	for t := range tagSet {
		if strings.Contains(t, targetLower) || strings.Contains(targetLower, t) || matchesFuzzy(t, targetLower) {
			similar = append(similar, t)
		}
	}
	return similar
}
