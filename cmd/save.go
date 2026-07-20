package cmd

import (
	"cwm/db"
	"fmt"
	"os"
	"strings"
	"time"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var saveCmd = &cobra.Command{
	Use:                "save [variable=command] [variable=command] ...",
	Short:              "Save command aliases",
	Long:               `Save one or more commands under variable aliases with tags. Supports setting tags positionally.`,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		// Handle help manually
		for _, arg := range args {
			if arg == "-h" || arg == "--help" {
				cmd.Help()
				return
			}
		}

		// Parse action flags manually
		var editFlag bool
		var editVarnameFlag bool
		var cleanArgs []string

		for _, arg := range args {
			switch arg {
case "-e", "--edit":
				editFlag = true
			case "-ev", "--ev", "--rename":
				editVarnameFlag = true
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

		// 1. Rename Mode (-ev)
		if editVarnameFlag {
			if len(cleanArgs) != 2 {
				fmt.Println(color.RedString("Error: Renaming requires exactly 2 arguments: old_var new_var (e.g. cwm save -ev old_name new_name)"))
				os.Exit(1)
			}
			oldVar := strings.TrimSpace(cleanArgs[0])
			newVar := strings.TrimSpace(cleanArgs[1])

			// Check if oldVar exists
			var exists bool
			err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", oldVar).Scan(&exists)
			if err != nil || !exists {
				fmt.Printf(color.RedString("Error: Variable '%s' not found.\n"), oldVar)
				os.Exit(1)
			}

			// Check if newVar already exists
			err = database.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", newVar).Scan(&exists)
			if err == nil && exists {
				fmt.Printf(color.RedString("Error: Variable '%s' already exists.\n"), newVar)
				os.Exit(1)
			}

			// Update variable name
			_, err = database.Exec("UPDATE saved_commands SET variable = ?, updated_at = ? WHERE variable = ?", newVar, time.Now(), oldVar)
			if err != nil {
				fmt.Printf(color.RedString("Error renaming variable: %v\n"), err)
				os.Exit(1)
			}

			_ = db.SyncToCopyBank(database)
			fmt.Printf(color.GreenString("Renamed variable ")+"%s -> %s\n", oldVar, newVar)
			return
		}

		if len(cleanArgs) == 0 {
			fmt.Println(color.RedString("Error: At least one command (e.g. alias=\"command\") is required."))
			cmd.Usage()
			os.Exit(1)
		}

		type commandToSave struct {
			variable string
			value    string
			tags     string
		}

		var commands []commandToSave
		var currentTags string

		for i := 0; i < len(cleanArgs); i++ {
			arg := cleanArgs[i]

			// Handle tags flags
			if arg == "-t" || arg == "--tags" || arg == "--tag" {
				if i+1 >= len(cleanArgs) {
					fmt.Printf(color.RedString("Error: Tag value missing after %s\n"), arg)
					os.Exit(1)
				}
				tagVal := strings.ToLower(strings.TrimSpace(cleanArgs[i+1]))
				i++

				currentTags = tagVal
				// Apply to the immediate preceding command
				if len(commands) > 0 {
					commands[len(commands)-1].tags = tagVal
				}
				continue
			}

			// Handle inline tags e.g. --tags=docker
			if strings.HasPrefix(arg, "-t=") || strings.HasPrefix(arg, "--tags=") || strings.HasPrefix(arg, "--tag=") {
				parts := strings.SplitN(arg, "=", 2)
				tagVal := strings.ToLower(strings.TrimSpace(parts[1]))
				currentTags = tagVal
				// Apply to the immediate preceding command
				if len(commands) > 0 {
					commands[len(commands)-1].tags = tagVal
				}
				continue
			}

			// Parse command: either var=val or var val
			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg, "=", 2)
				variable := strings.TrimSpace(parts[0])
				value := cleanValue(parts[1])

				if variable == "" || value == "" {
					fmt.Printf(color.RedString("Error: Invalid parameter format: %s\n"), arg)
					os.Exit(1)
				}

				commands = append(commands, commandToSave{
					variable: variable,
					value:    value,
					tags:     currentTags,
				})
			} else {
				// Try positional alias command
				if i+1 < len(cleanArgs) && !strings.HasPrefix(cleanArgs[i+1], "-") && !strings.Contains(cleanArgs[i+1], "=") {
					variable := strings.TrimSpace(arg)
					value := cleanValue(cleanArgs[i+1])
					i++
					commands = append(commands, commandToSave{
						variable: variable,
						value:    value,
						tags:     currentTags,
					})
				} else {
					fmt.Printf(color.RedString("Error: Unparseable argument: %s\n"), arg)
					os.Exit(1)
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

		for _, c := range commands {
			var exists bool
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM saved_commands WHERE variable = ?)", c.variable).Scan(&exists)
			if err != nil {
				tx.Rollback()
				fmt.Printf(color.RedString("Database query error: %v\n"), err)
				os.Exit(1)
			}

			if editFlag {
				// Edit mode: Must exist
				if !exists {
					tx.Rollback()
					fmt.Printf(color.RedString("Error: Variable '%s' does not exist.\n"), c.variable)
					os.Exit(1)
				}
				_, err = tx.Exec(`
					UPDATE saved_commands SET command = ?, tags = ?, updated_at = ? WHERE variable = ?
				`, c.value, c.tags, now, c.variable)
			} else {
				// Normal save: Must NOT exist
				if exists {
					tx.Rollback()
					fmt.Printf(color.RedString("Error: Variable '%s' already exists. (Use -e to edit)\n"), c.variable)
					os.Exit(1)
				}
				_, err = tx.Exec(`
					INSERT INTO saved_commands (variable, command, tags, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?)
				`, c.variable, c.value, c.tags, now, now)
			}

			if err != nil {
				tx.Rollback()
				fmt.Printf(color.RedString("Error saving command '%s': %v\n"), c.variable, err)
				os.Exit(1)
			}
		}

		if err := tx.Commit(); err != nil {
			fmt.Printf(color.RedString("Transaction commit error: %v\n"), err)
			os.Exit(1)
		}

		// Sync to copy bank
		if err := db.SyncToCopyBank(database); err != nil {
			fmt.Printf(color.YellowString("Copy Bank Sync warning: %v\n"), err)
		}

		for _, c := range commands {
			actionWord := "Saved"
			if editFlag {
				actionWord = "Updated"
			}
			if c.tags != "" {
				fmt.Printf(color.GreenString("%s %s ")+"[%s] -> %s\n", actionWord, c.variable, c.tags, c.value)
			} else {
				fmt.Printf(color.GreenString("%s %s ")+"-> %s\n", actionWord, c.variable, c.value)
			}
		}

		if len(commands) > 0 {
			fmt.Println()
			fmt.Printf(color.HiBlackString("Hint: Run 'cwm get %s' or 'cwm get' to retrieve.\n"), commands[0].variable)
		}
	},
}

func init() {
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
