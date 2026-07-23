package cmd

import (
	"cwm/db"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var saveCmd = &cobra.Command{
	Use:                "save [variable_name] [command] [flags]",
	Short:              "Save command aliases or multiline scripts",
	Long:               `Save one or more commands under variable aliases with tags, descriptions, history lookup (--last), stdin piping, or multiline script editor (-s).`,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		// Handle help manually
		for _, arg := range args {
			if arg == "-h" || arg == "--help" {
				cmd.Help()
				return
			}
		}

		// Parse action and configuration flags manually
		var editFlag bool
		var editVarnameFlag bool
		var lastFlag bool
		var scriptFlag bool
		var descFlagPassed bool
		var descriptionVal string
		var tagsFlagPassed bool
		var currentTags string
		var cleanArgs []string

		for i := 0; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "-e" || arg == "--edit":
				editFlag = true
			case arg == "-ev" || arg == "--ev" || arg == "-r" || arg == "--rename":
				editVarnameFlag = true
			case arg == "-l" || arg == "--last":
				lastFlag = true
			case arg == "-s" || arg == "--script":
				scriptFlag = true
			case arg == "-d" || arg == "--desc" || arg == "--description":
				descFlagPassed = true
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					descriptionVal = strings.TrimSpace(args[i+1])
					i++
				} else {
					descriptionVal = ""
				}
			case strings.HasPrefix(arg, "-d=") || strings.HasPrefix(arg, "--desc=") || strings.HasPrefix(arg, "--description="):
				descFlagPassed = true
				parts := strings.SplitN(arg, "=", 2)
				descriptionVal = strings.TrimSpace(parts[1])
			case arg == "-t" || arg == "--tags" || arg == "--tag":
				tagsFlagPassed = true
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					currentTags = strings.ToLower(strings.TrimSpace(args[i+1]))
					i++
				} else {
					currentTags = ""
				}
			case strings.HasPrefix(arg, "-t=") || strings.HasPrefix(arg, "--tags=") || strings.HasPrefix(arg, "--tag="):
				tagsFlagPassed = true
				parts := strings.SplitN(arg, "=", 2)
				currentTags = strings.ToLower(strings.TrimSpace(parts[1]))
			default:
				cleanArgs = append(cleanArgs, arg)
			}
		}

		if editFlag && editVarnameFlag {
			fmt.Println(color.RedString("Error: Only one action flag allowed (-e or -ev)."))
			os.Exit(1)
		}

		// Connect to DB
		database, err := db.InitDB()
		if err != nil {
			fmt.Printf(color.RedString("Database error: %v\n"), err)
			os.Exit(1)
		}
		defer database.Close()

		// 1. Rename Mode (-ev / -r)
		if editVarnameFlag {
			if len(cleanArgs) != 2 {
				fmt.Println(color.RedString("Error: Renaming requires exactly 2 arguments: old_var new_var (e.g. cwm save -r old_name new_name)"))
				os.Exit(1)
			}
			oldVar := strings.TrimSpace(cleanArgs[0])
			newVar := strings.TrimSpace(cleanArgs[1])

			var exists bool
			err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", oldVar).Scan(&exists)
			if err != nil || !exists {
				fmt.Printf(color.RedString("Error: Variable '%s' not found.\n"), oldVar)
				os.Exit(1)
			}

			err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", newVar).Scan(&exists)
			if err == nil && exists {
				fmt.Printf(color.RedString("Error: Variable '%s' already exists.\n"), newVar)
				os.Exit(1)
			}

			_, err = database.Exec("UPDATE saved_commands SET variable = ?, updated_at = ? WHERE variable = ?", newVar, time.Now(), oldVar)
			if err != nil {
				fmt.Printf(color.RedString("Error renaming variable: %v\n"), err)
				os.Exit(1)
			}

			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("Renamed variable ")+"%s -> %s\n", oldVar, newVar)
			return
		}

		type commandToSave struct {
			variable    string
			value       string
			tags        string
			description string
		}

		var commands []commandToSave

		// Check if stdin is piped
		stdinContent, stdinIsPiped := readStdinIfPiped()

		// 2. Script Mode (-s / --script)
		if scriptFlag {
			if len(cleanArgs) == 0 {
				fmt.Println(color.RedString("Error: Variable name required when saving a script (e.g. cwm save -s my_script)"))
				os.Exit(1)
			}
			varName := strings.TrimSpace(cleanArgs[0])

			scriptsFolder, errScripts := db.GetScriptsDir()
			if errScripts != nil {
				fmt.Printf(color.RedString("Error resolving scripts directory: %v\n"), errScripts)
				os.Exit(1)
			}

			shType := detectShell()
			ext := ".sh"
			runner := "bash"
			if shType == "pwsh" || shType == "powershell" || runtime.GOOS == "windows" {
				ext = ".ps1"
				runner = "powershell -ExecutionPolicy Bypass -File"
			}

			scriptPath := filepath.Join(scriptsFolder, varName+ext)

			var prefillContent string
			var exists bool
			_ = database.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", varName).Scan(&exists)

			if editFlag || exists {
				data, errRead := os.ReadFile(scriptPath)
				if errRead == nil {
					prefillContent = string(data)
				} else {
					var dbCmd string
					_ = database.QueryRow("SELECT command FROM saved_commands WHERE variable = ?", varName).Scan(&dbCmd)
					prefillContent = dbCmd
				}
			}

			if !editFlag && exists {
				fmt.Printf(color.YellowString("Warning: Variable '%s' already exists. Editing script content.\n"), varName)
			}

			launchNativeEditor(database, varName, scriptPath, prefillContent)

			formattedPath := filepath.ToSlash(scriptPath)
			cmdVal := fmt.Sprintf("%s \"%s\"", runner, formattedPath)

			tagList := "script"
			if currentTags != "" {
				if !strings.Contains(currentTags, "script") {
					tagList = currentTags + ", script"
				} else {
					tagList = currentTags
				}
			}

			descVal := descriptionVal
			if descVal == "" {
				descVal = "Script: " + varName
			}

			commands = append(commands, commandToSave{
				variable:    varName,
				value:       cmdVal,
				tags:        tagList,
				description: descVal,
			})
		} else if editFlag && len(cleanArgs) == 1 && !lastFlag && !stdinIsPiped {
			// Metadata-only edit mode: cwm save -e hello -d "simple" -t "sg"
			varName := strings.TrimSpace(cleanArgs[0])
			var existingCmd, existingTags, existingDesc string
			errScan := database.QueryRow("SELECT command, tags, description FROM saved_commands WHERE variable = ?", varName).Scan(&existingCmd, &existingTags, &existingDesc)
			if errScan == sql.ErrNoRows {
				fmt.Printf(color.RedString("Error: Variable '%s' does not exist.\n"), varName)
				os.Exit(1)
			} else if errScan != nil {
				fmt.Printf(color.RedString("Database query error: %v\n"), errScan)
				os.Exit(1)
			}

			finalTags := existingTags
			if tagsFlagPassed {
				finalTags = currentTags
			}
			finalDesc := existingDesc
			if descFlagPassed {
				finalDesc = descriptionVal
			}

			commands = append(commands, commandToSave{
				variable:    varName,
				value:       existingCmd,
				tags:        finalTags,
				description: finalDesc,
			})
		} else if lastFlag {
			// Save last command executed in shell history
			if len(cleanArgs) == 0 {
				fmt.Println(color.RedString("Error: Alias name required when saving from history (e.g., cwm save <alias> --last)"))
				os.Exit(1)
			}
			aliasName := strings.TrimSpace(cleanArgs[0])
			lastCmd, errLast := getLastHistoryCommand(database)
			if errLast != nil {
				fmt.Printf(color.RedString("Error fetching last command: %v\n"), errLast)
				os.Exit(1)
			}
			commands = append(commands, commandToSave{
				variable:    aliasName,
				value:       lastCmd,
				tags:        currentTags,
				description: descriptionVal,
			})
		} else if len(cleanArgs) == 1 && stdinIsPiped {
			// Multi-line / Standard input piping (e.g. cat deploy.sh | cwm save deploy-script)
			aliasName := strings.TrimSpace(cleanArgs[0])
			commands = append(commands, commandToSave{
				variable:    aliasName,
				value:       stdinContent,
				tags:        currentTags,
				description: descriptionVal,
			})
		} else {
			if len(cleanArgs) == 0 {
				fmt.Println(color.RedString("Error: At least one command (e.g. alias=\"command\" or alias command) is required."))
				cmd.Usage()
				os.Exit(1)
			}

			for i := 0; i < len(cleanArgs); i++ {
				arg := cleanArgs[i]

				if strings.Contains(arg, "=") {
					parts := strings.SplitN(arg, "=", 2)
					variable := strings.TrimSpace(parts[0])
					value := cleanValue(parts[1])

					if variable == "" || value == "" {
						fmt.Printf(color.RedString("Error: Invalid parameter format: %s\n"), arg)
						os.Exit(1)
					}

					commands = append(commands, commandToSave{
						variable:    variable,
						value:       value,
						tags:        currentTags,
						description: descriptionVal,
					})
				} else {
					if i+1 < len(cleanArgs) {
						variable := strings.TrimSpace(arg)
						value := cleanValue(cleanArgs[i+1])
						i++
						commands = append(commands, commandToSave{
							variable:    variable,
							value:       value,
							tags:        currentTags,
							description: descriptionVal,
						})
					} else if stdinIsPiped {
						variable := strings.TrimSpace(arg)
						commands = append(commands, commandToSave{
							variable:    variable,
							value:       stdinContent,
							tags:        currentTags,
							description: descriptionVal,
						})
					} else {
						fmt.Printf(color.RedString("Error: Missing command value for variable '%s'. Usage: cwm save %s \"<command>\"\n"), arg, arg)
						os.Exit(1)
					}
				}
			}
		}

		if len(commands) == 0 {
			fmt.Println(color.RedString("Error: No valid commands parsed to save."))
			os.Exit(1)
		}

		tx, err := database.Begin()
		if err != nil {
			fmt.Printf(color.RedString("Transaction error: %v\n"), err)
			os.Exit(1)
		}

		now := time.Now()

		for idx := range commands {
			c := &commands[idx]
			var exists bool
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", c.variable).Scan(&exists)
			if err != nil {
				tx.Rollback()
				fmt.Printf(color.RedString("Database query error: %v\n"), err)
				os.Exit(1)
			}

			if editFlag {
				if !exists {
					tx.Rollback()
					fmt.Printf(color.RedString("Error: Variable '%s' does not exist.\n"), c.variable)
					os.Exit(1)
				}

				var existingTags, existingDesc string
				_ = tx.QueryRow("SELECT tags, description FROM saved_commands WHERE variable = ?", c.variable).Scan(&existingTags, &existingDesc)

				finalTags := c.tags
				if !tagsFlagPassed {
					finalTags = existingTags
				}
				finalDesc := c.description
				if !descFlagPassed {
					finalDesc = existingDesc
				}

				_, err = tx.Exec(`
					UPDATE saved_commands SET command = ?, tags = ?, description = ?, updated_at = ? WHERE variable = ?
				`, c.value, finalTags, finalDesc, now, c.variable)

				c.tags = finalTags
				c.description = finalDesc
			} else {
				if exists {
					// If script mode, allow updating existing script entry gracefully
					if scriptFlag {
						_, err = tx.Exec(`
							UPDATE saved_commands SET command = ?, tags = ?, description = ?, updated_at = ? WHERE variable = ?
						`, c.value, c.tags, c.description, now, c.variable)
					} else {
						tx.Rollback()
						fmt.Printf(color.RedString("Error: Variable '%s' already exists. (Use -e to edit)\n"), c.variable)
						os.Exit(1)
					}
				} else {
					_, err = tx.Exec(`
						INSERT INTO saved_commands (variable, command, tags, description, created_at, updated_at)
						VALUES (?, ?, ?, ?, ?, ?)
					`, c.variable, c.value, c.tags, c.description, now, now)
				}
			}

			if err != nil {
				tx.Rollback()
				fmt.Printf(color.RedString("Error saving command '%s': %v\n"), c.variable, err)
				os.Exit(1)
			}
		}

		if err = tx.Commit(); err != nil {
			fmt.Printf(color.RedString("Transaction commit error: %v\n"), err)
			os.Exit(1)
		}

		_ = db.SyncToCopyBank(database)

		for _, c := range commands {
			actionLabel := color.GreenString("Saved")
			if editFlag {
				actionLabel = color.GreenString("Updated")
			}

			tagPrefix := ""
			if c.tags != "" {
				tagPrefix = fmt.Sprintf("[%s] ", color.CyanString(c.tags))
			}

			cmdVal := c.value
			if strings.Contains(cmdVal, "\n") {
				cmdVal = strings.Split(cmdVal, "\n")[0] + "..."
			}

			descStr := ""
			if c.description != "" {
				d := c.description
				if len(d) > 30 {
					d = d[:27] + "..."
				}
				descStr = fmt.Sprintf(" (%s)", color.YellowString(d))
			}

			if len(cmdVal) > 40 && descStr != "" {
				cmdVal = cmdVal[:37] + "..."
			}

			fmt.Printf("%s %s%s = %s%s\n", actionLabel, tagPrefix, color.New(color.Bold).Sprint(c.variable), cmdVal, descStr)
		}
	},
}

func resolveSystemEditor(database *sql.DB) (string, []string) {
	if database != nil {
		ed, _ := db.GetConfigValue(database, "editor")
		if strings.TrimSpace(ed) != "" {
			parts := strings.Fields(strings.TrimSpace(ed))
			return parts[0], parts[1:]
		}
	}

	for _, env := range []string{"EDITOR", "VISUAL"} {
		ed := strings.TrimSpace(os.Getenv(env))
		if ed != "" {
			parts := strings.Fields(ed)
			return parts[0], parts[1:]
		}
	}

	if runtime.GOOS == "windows" {
		return "notepad", nil
	}

	for _, app := range []string{"nano", "vim", "vi"} {
		if _, err := exec.LookPath(app); err == nil {
			return app, nil
		}
	}

	return "vi", nil
}

func launchNativeEditor(database *sql.DB, varName string, filePath string, prefill string) string {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf(color.RedString("Error creating script directory %s: %v\n"), dir, err)
		os.Exit(1)
	}

	if prefill != "" {
		_ = os.WriteFile(filePath, []byte(prefill), 0644)
	} else if _, err := os.Stat(filePath); os.IsNotExist(err) {
		defaultHeader := `# --- CWM Script Header ---
# Example Inline Variables: %name%, %greeting%
# When executed (cwm get ` + varName + ` -x), CWM will automatically prompt for values.
# -------------------------

`
		if strings.HasSuffix(filePath, ".sh") {
			defaultHeader = `#!/usr/bin/env bash
# --- CWM Script Header ---
# Example Inline Variables: %name%, %greeting%
# When executed (cwm get ` + varName + ` -x), CWM will automatically prompt for values.
# -------------------------

`
		}
		_ = os.WriteFile(filePath, []byte(defaultHeader), 0644)
	}

	editorCmd, editorArgs := resolveSystemEditor(database)
	fullArgs := append(editorArgs, filePath)

	fmt.Printf(color.CyanString("Opened script editor (%s)... "), editorCmd)
	fmt.Println("Edit and save your script file anytime!")

	cmd := exec.Command(editorCmd, fullArgs...)
	_ = cmd.Start()

	return filePath
}

func readStdinIfPiped() (string, bool) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", false
	}
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false
		}
		content := string(bytes)
		return content, len(content) > 0
	}
	return "", false
}

func getLastHistoryCommand(database *sql.DB) (string, error) {
	histPath := getHistoryFilePath(database)
	if histPath == "" {
		return "", fmt.Errorf("could not detect system history file")
	}

	lines, err := readLines(histPath)
	if err != nil {
		return "", fmt.Errorf("could not read history file: %w", err)
	}

	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
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
		return line, nil
	}

	return "", fmt.Errorf("no previous shell history commands found")
}

func init() {
	saveCmd.Flags().Bool("help", false, "help for save")
	saveCmd.Flags().StringP("desc", "d", "", "Attach a description string to the record")
	saveCmd.Flags().BoolP("edit", "e", false, "Edit an existing saved command or script")
	saveCmd.Flags().BoolP("last", "l", false, "Save last executed command from shell history")
	saveCmd.Flags().BoolP("rename", "r", false, "Rename an existing variable alias")
	saveCmd.Flags().BoolP("script", "s", false, "Create or edit a multiline script in ~/.cwm/scripts/")
	saveCmd.Flags().StringP("tag", "t", "", "Filter or attach tags")
	rootCmd.AddCommand(saveCmd)
}

func cleanValue(val string) string {
	val = strings.TrimSpace(val)
	for {
		original := val
		// Strip escaping quotes
		val = strings.ReplaceAll(val, `\"`, `"`)
		val = strings.ReplaceAll(val, `\'`, `'`)

		// Strip surrounding quotes and slashes
		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
			(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) ||
			(strings.HasPrefix(val, `\`) && strings.HasSuffix(val, `\`)) {
			if len(val) >= 2 {
				val = val[1 : len(val)-1]
			}
		}
		val = strings.TrimSpace(val)
		if val == original {
			break
		}
	}
	return val
}
