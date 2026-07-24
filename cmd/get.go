package cmd

import (
	"bufio"
	"cwm/db"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	execFlag   bool
)

var placeholderRegex = regexp.MustCompile(`%[a-zA-Z0-9_]+%|\{\{[a-zA-Z0-9_]+\}\}`)

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

func stripCommentedLines(text string) string {
	lines := strings.Split(text, "\n")
	var codeLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "rem ") || strings.HasPrefix(trimmed, "REM ") {
			continue
		}
		codeLines = append(codeLines, l)
	}
	return strings.Join(codeLines, "\n")
}

func extractScriptPlaceholders(cmdStr string) ([]string, string) {
	var fileMatches []string
	var scriptPath string
	if strings.Contains(cmdStr, "scripts/") || strings.Contains(cmdStr, "scripts\\") || strings.Contains(cmdStr, ".sh") || strings.Contains(cmdStr, ".ps1") {
		words := strings.Fields(cmdStr)
		for _, w := range words {
			cleanW := strings.Trim(w, `"'`)
			if strings.HasSuffix(cleanW, ".sh") || strings.HasSuffix(cleanW, ".ps1") {
				scriptPath = cleanW
				if data, err := os.ReadFile(cleanW); err == nil {
					cleanCode := stripCommentedLines(string(data))
					fileMatches = placeholderRegex.FindAllString(cleanCode, -1)
				}
				break
			}
		}
	}
	return fileMatches, scriptPath
}

func resolvePlaceholders(cmdStr string) string {
	cleanCmd := stripCommentedLines(cmdStr)
	matches := placeholderRegex.FindAllString(cleanCmd, -1)
	scriptMatches, _ := extractScriptPlaceholders(cmdStr)
	matches = append(matches, scriptMatches...)

	if len(matches) == 0 {
		return cmdStr
	}

	seen := make(map[string]bool)
	var uniqueVars []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			uniqueVars = append(uniqueVars, m)
		}
	}

	reader := bufio.NewReader(os.Stdin)
	replaced := cmdStr

	for _, varPlaceholder := range uniqueVars {
		fmt.Fprintf(os.Stderr, "Enter value for %s: ", color.CyanString(varPlaceholder))
		input, _ := reader.ReadString('\n')
		input = strings.TrimRight(input, "\r\n")

		if strings.Contains(replaced, varPlaceholder) {
			replaced = strings.ReplaceAll(replaced, varPlaceholder, input)
		} else {
			replaced = fmt.Sprintf("%s \"%s\"", replaced, input)
		}
	}
	return replaced
}

func extractScriptPath(cmdStr string) string {
	if strings.Contains(cmdStr, "scripts/") || strings.Contains(cmdStr, "scripts\\") || strings.Contains(cmdStr, ".sh") || strings.Contains(cmdStr, ".ps1") {
		words := strings.Fields(cmdStr)
		for _, w := range words {
			cleanW := strings.Trim(w, `"'`)
			if strings.HasSuffix(cleanW, ".sh") || strings.HasSuffix(cleanW, ".ps1") {
				return cleanW
			}
		}
	}
	return ""
}

func validateScriptFileExists(cmdStr string) (string, bool) {
	scriptPath := extractScriptPath(cmdStr)
	if scriptPath != "" {
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			return scriptPath, false
		}
		return scriptPath, true
	}
	return "", true
}

func outputOrCopyCommand(database *sql.DB, varName string, cmdStr string, descStr string, cmdType string, choiceLabel string) {
	// ONLY check script file existence on disk when -x / --exec is specified!
	if execFlag {
		if cmdType == "script" || strings.Contains(cmdStr, "scripts/") || strings.Contains(cmdStr, "scripts\\") || strings.HasSuffix(cmdStr, ".ps1") || strings.HasSuffix(cmdStr, ".sh") {
			scriptPath, valid := validateScriptFileExists(cmdStr)
			if !valid {
				fmt.Fprintf(os.Stderr, color.RedString("\nScript file missing for '%s' at: %s\n"), varName, scriptPath)
				fmt.Fprintf(os.Stderr, "Options:\n")
				fmt.Fprintf(os.Stderr, "  %s Delete saved script command entry '%s'\n", color.CyanString("[d]"), varName)
				fmt.Fprintf(os.Stderr, "  %s Create new script file with default template\n", color.CyanString("[c]"))
				fmt.Fprintf(os.Stderr, "  %s Cancel\n", color.CyanString("[N]"))
				fmt.Fprintf(os.Stderr, "\nChoose option [d/c/N]: ")

				reader := bufio.NewReader(os.Stdin)
				choice, _ := reader.ReadString('\n')
				choice = strings.TrimSpace(strings.ToLower(choice))

				switch choice {
				case "d", "delete":
					if database != nil && varName != "" {
						_ = db.TrashSavedCommands(database, []string{varName})
						_, _ = database.Exec("DELETE FROM saved_commands WHERE variable = ?", varName)
						_ = db.SyncToCopyBank(database)
						fmt.Fprintf(os.Stderr, "%s", color.GreenString("Deleted saved script entry '%s' (archived to trash).\n", varName))
					} else {
						fmt.Fprintln(os.Stderr, color.YellowString("Could not delete saved command (variable name unknown)."))
					}
					return
				case "c", "create":
					// Re-create script file with default template and launch native editor
					if scriptPath != "" {
						ext := filepath.Ext(scriptPath)
						isPwsh := ext == ".ps1"
						var defaultContent string
						if isPwsh {
							defaultContent = fmt.Sprintf("# Script: %s\n# Auto-generated by CWM\n\nparam (\n    [string]$Path = \".\"\n)\n\nWrite-Host \"Executing %s on $Path\"\n", varName, varName)
						} else {
							defaultContent = fmt.Sprintf("#!/bin/env bash\n# Script: %s\n# Auto-generated by CWM\n\nPath=\"${1:-.}\"\necho \"Executing %s on $Path\"\n", varName, varName)
						}

						dir := filepath.Dir(scriptPath)
						_ = os.MkdirAll(dir, 0755)
						_ = os.WriteFile(scriptPath, []byte(defaultContent), 0644)

						fmt.Fprintf(os.Stderr, "%s", color.GreenString("Created missing script file: %s\n", scriptPath))
						if database != nil && varName != "" {
							launchNativeEditor(database, varName, scriptPath, defaultContent)
						}
					}
					return
				default:
					fmt.Fprintln(os.Stderr, "Cancelled.")
					return
				}
			}
		}

		cmdStr = resolvePlaceholders(cmdStr)

		if descStr != "" {
			fmt.Fprintf(os.Stderr, "%s %s\n", color.New(color.Bold, color.FgCyan).Sprint("Description:"), descStr)
		}

		trimmedCmd := strings.TrimSpace(cmdStr)
		if strings.HasPrefix(trimmedCmd, "cd ") || strings.HasPrefix(trimmedCmd, "set ") || strings.HasPrefix(trimmedCmd, "export ") || strings.HasPrefix(trimmedCmd, "$env:") {
			fmt.Fprintln(os.Stderr, color.YellowString("Notice: Running 'cd' or environment commands via -x requires shell profile setup to change your active prompt."))
			fmt.Fprintln(os.Stderr, "Run "+color.GreenString("cwm setup")+" and reload your profile to enable native prompt navigation.\n")
		}

		fmt.Fprintln(os.Stderr, color.GreenString("Executing command: ")+cmdStr)
		executeCommandInTerminal(cmdStr)
		return
	}

	cmdStr = resolvePlaceholders(cmdStr)

	if showFlag {
		fmt.Print(cmdStr)
		if !strings.HasSuffix(cmdStr, "\n") {
			fmt.Println()
		}
		return
	}

	if err := clipboard.WriteAll(cmdStr); err != nil {
		fmt.Printf(color.YellowString("Warning: Failed to copy to clipboard: %v\n"), err)
		if descStr != "" {
			fmt.Printf("%s %s\n", color.New(color.Bold, color.FgCyan).Sprint("Description:"), descStr)
		}
		fmt.Print(cmdStr)
		if !strings.HasSuffix(cmdStr, "\n") {
			fmt.Println()
		}
	} else {
		if descStr != "" {
			fmt.Printf("%s %s\n", color.New(color.Bold, color.FgCyan).Sprint("Description:"), descStr)
		}
		if choiceLabel != "" {
			fmt.Printf(color.GreenString("Copied command #%s -> "), choiceLabel)
		} else {
			fmt.Print(color.GreenString("Copied to clipboard! "))
		}
		if strings.Contains(cmdStr, "\n") {
			lines := strings.Split(cmdStr, "\n")
			fmt.Printf("%s ... [%d lines]\n", lines[0], len(lines))
		} else {
			fmt.Println(cmdStr)
		}
	}
}

func parseCommandArgs(cmdStr string) (string, []string) {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range cmdStr {
		switch {
		case inQuote:
			if r == quoteChar {
				inQuote = false
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			inQuote = true
			quoteChar = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

func executeCommandInTerminal(cmdStr string) {
	shType := detectShell()
	var execCmd *exec.Cmd

	trimmed := strings.TrimSpace(cmdStr)

	// If command is an explicit shell executable invocation (e.g. powershell -ExecutionPolicy ... or bash path/to/script.sh), run directly!
	if strings.HasPrefix(trimmed, "powershell ") || strings.HasPrefix(trimmed, "pwsh ") || strings.HasPrefix(trimmed, "bash ") || strings.HasPrefix(trimmed, "zsh ") {
		bin, args := parseCommandArgs(trimmed)
		execCmd = exec.Command(bin, args...)
	} else if runtime.GOOS == "windows" {
		if shType == "pwsh" || shType == "powershell" {
			execCmd = exec.Command("powershell", "-NoProfile", "-Command", cmdStr)
		} else {
			execCmd = exec.Command("cmd.exe", "/c", cmdStr)
		}
	} else {
		if shType == "zsh" {
			execCmd = exec.Command("zsh", "-c", cmdStr)
		} else {
			execCmd = exec.Command("bash", "-c", cmdStr)
		}
	}

	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	if err := execCmd.Run(); err != nil {
		fmt.Printf(color.RedString("\nCommand finished with error: %v\n"), err)
	}
}

func handleSingleFetch(database *sql.DB, search string) {
	var commandVal, descVal, cmdType string
	// Query by variable name directly
	err := database.QueryRow("SELECT command, description, COALESCE(type, 'command') FROM saved_commands WHERE variable = ?", search).Scan(&commandVal, &descVal, &cmdType)

	if err == nil {
		outputOrCopyCommand(database, search, commandVal, descVal, cmdType, "")
		return
	}

	if err != sql.ErrNoRows {
		fmt.Printf(color.RedString("Error searching command: %v\n"), err)
		os.Exit(1)
	}

	// Exact match not found: check if any fuzzy/substring suggestions exist
	rows, errQuery := database.Query("SELECT variable, command, tags, description FROM saved_commands")
	if errQuery == nil {
		defer rows.Close()
		searchLower := strings.ToLower(search)
		matchCount := 0
		for rows.Next() {
			var v, c, t, d string
			if errScan := rows.Scan(&v, &c, &t, &d); errScan == nil {
				if strings.Contains(strings.ToLower(v), searchLower) || matchesFuzzy(v, searchLower) ||
					strings.Contains(strings.ToLower(c), searchLower) || matchesFuzzy(c, searchLower) ||
					strings.Contains(strings.ToLower(d), searchLower) || matchesFuzzy(d, searchLower) {
					matchCount++
				}
			}
		}
		if err := rows.Err(); err == nil && matchCount > 0 {
			fmt.Printf(color.YellowString("Command '%s' not found. Showing matching suggestions:\n"), search)
			handleSavedListLookup(database, search)
			return
		}
	}

	fmt.Printf(color.RedString("Error: Command '%s' not found.\n"), search)
	os.Exit(1)
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

	if search != "" {
		pattern := fuzzyPattern(search)
		query := "SELECT variable, command, tags, description, COALESCE(type, 'command') FROM saved_commands WHERE variable LIKE ? OR command LIKE ? OR description LIKE ? OR tags LIKE ? ORDER BY created_at ASC"
		rows, err = database.Query(query, pattern, pattern, pattern, pattern)
		if err != nil || !rows.Next() {
			if rows != nil {
				rows.Close()
			}
			query = "SELECT variable, command, tags, description, COALESCE(type, 'command') FROM saved_commands ORDER BY created_at ASC"
			rows, err = database.Query(query)
		} else {
			rows.Close()
			rows, err = database.Query(query, pattern, pattern, pattern, pattern)
		}
	} else {
		query := "SELECT variable, command, tags, description, COALESCE(type, 'command') FROM saved_commands ORDER BY created_at ASC"
		rows, err = database.Query(query)
	}

	if err != nil {
		fmt.Printf(color.RedString("Error querying database: %v\n"), err)
		os.Exit(1)
	}
	defer rows.Close()

	type cmdItem struct {
		varName     string
		cmdStr      string
		tags        string
		description string
		cmdType     string
	}

	var items []cmdItem
	tagMatchedAny := false
	for rows.Next() {
		var item cmdItem
		if err := rows.Scan(&item.varName, &item.cmdStr, &item.tags, &item.description, &item.cmdType); err != nil {
			continue
		}

		if search != "" {
			searchLower := strings.ToLower(search)
			vMatch := strings.Contains(strings.ToLower(item.varName), searchLower) || matchesFuzzy(item.varName, searchLower)
			cMatch := strings.Contains(strings.ToLower(item.cmdStr), searchLower) || matchesFuzzy(item.cmdStr, searchLower)
			dMatch := strings.Contains(strings.ToLower(item.description), searchLower) || matchesFuzzy(item.description, searchLower)
			tMatch := strings.Contains(strings.ToLower(item.tags), searchLower) || matchesFuzzy(item.tags, searchLower)
			if !vMatch && !cMatch && !dMatch && !tMatch {
				continue
			}
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

	headerTitle := "Saved Commands"
	fmt.Printf("\n%s\n", color.New(color.Bold, color.Underline).Sprint(headerTitle))
	fmt.Printf("%-5s  %-15s  %-35s  %-15s  %-20s\n", "ID", "Variable", "Command", "Tags", "Description")
	fmt.Println(strings.Repeat("-", 95))

	type displayItem struct {
		varName     string
		cmdStr      string
		description string
		cmdType     string
	}

	displayMap := make(map[string]displayItem)
	for i, item := range items {
		displayId := i + 1
		idStr := strconv.Itoa(displayId)
		displayMap[idStr] = displayItem{
			varName:     item.varName,
			cmdStr:      item.cmdStr,
			description: item.description,
			cmdType:     item.cmdType,
		}

		cmdPreview := item.cmdStr
		if strings.Contains(cmdPreview, "\n") {
			cmdPreview = strings.Split(cmdPreview, "\n")[0] + "..."
		}
		if len(cmdPreview) > 35 {
			cmdPreview = cmdPreview[:32] + "..."
		}

		descPreview := item.description
		if len(descPreview) > 20 {
			descPreview = descPreview[:17] + "..."
		}

		fmt.Printf("%-5d  %-15s  %-35s  %-15s  %-20s\n", displayId, item.varName, cmdPreview, item.tags, descPreview)
	}
	fmt.Println()

	if listFlag {
		return
	}

	promptLabel := "Copy (ID): "
	if execFlag {
		promptLabel = "Execute (ID): "
	}
	fmt.Print(promptLabel)
	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" {
		return
	}

	if item, ok := displayMap[choice]; ok {
		outputOrCopyCommand(database, item.varName, item.cmdStr, item.description, item.cmdType, choice)
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
		outputOrCopyCommand(database, "", cmdStr, "", "command", choice)
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
	getCmd.Flags().BoolVarP(&execFlag, "exec", "x", false, "Execute retrieved command directly in terminal")
	getCmd.Flags().BoolVar(&showFlag, "show", false, "Show command value without copying")
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
