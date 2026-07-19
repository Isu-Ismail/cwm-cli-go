package cmd

import (
	"bufio"
	"cwm/db"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var forceSetupFlag bool

const (
	bashConfig = `
# --- CWM History Setup ---
# Append to history file immediately, don't overwrite
shopt -s histappend
# Instant write to disk after every command
export PROMPT_COMMAND="history -a; $PROMPT_COMMAND"
# Ignore duplicate commands and commands starting with space
export HISTCONTROL=ignoreboth
`
	zshConfig = `
# --- CWM History Setup ---
HISTFILE="$HOME/.zsh_history"
# Keep 50k commands in memory and on disk
HISTSIZE=50000
SAVEHIST=50000
# Write commands immediately after each execution
setopt INC_APPEND_HISTORY
# Ignore duplicates and commands starting with space
setopt HIST_IGNORE_DUPS
setopt HIST_IGNORE_ALL_DUPS
setopt HIST_IGNORE_SPACE
# Disable extended timestamp format (Clean raw commands)
unsetopt EXTENDED_HISTORY
setopt NO_EXTENDED_HISTORY
`
	pwshConfig = `
# --- CWM History Setup ---
# Ensure commands are saved immediately
Set-PSReadLineOption -HistorySaveStyle SaveIncrementally
# Prevent duplicates in history
Set-PSReadLineOption -HistoryNoDuplicates
`
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure shell for history synchronization",
	Long:  `Configures your shell (Bash, Zsh, or PowerShell) profile for instant history synchronization and deduplication.`,
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf(color.RedString("Error getting home directory: %v\n"), err)
			os.Exit(1)
		}

		bashrc := filepath.Join(home, ".bashrc")
		zshrc := filepath.Join(home, ".zshrc")

		if forceSetupFlag {
			fmt.Println("\n" + color.New(color.Bold).Sprint("Manual Setup Mode"))
			fmt.Println("  1) Bash (Linux / Mac / Git Bash)")
			fmt.Println("  2) Zsh (Linux / Mac)")
			fmt.Println("  3) PowerShell (Windows)")
			fmt.Println()

			reader := bufio.NewReader(os.Stdin)
			fmt.Print("Select shell (1-3): ")
			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(choice)

			switch choice {
			case "1":
				appendConfigBlock(bashrc, bashConfig, "Bash")
			case "2":
				appendConfigBlock(zshrc, zshConfig, "Zsh")
			case "3":
				setupPowershellHistory()
			default:
				fmt.Println(color.RedString("Invalid choice."))
			}
			return
		}

		// Auto-Detection
		shellType := detectShell()
		if runtime.GOOS == "windows" {
			isGitBash := os.Getenv("MSYSTEM") != "" || strings.Contains(strings.ToLower(os.Getenv("SHELL")), "bash")
			if isGitBash {
				fmt.Println(color.CyanString("Detected Git Bash."))
				appendConfigBlock(bashrc, bashConfig, "Git Bash")
			} else {
				fmt.Println(color.CyanString("Detected Windows System."))
				setupPowershellHistory()
			}
			return
		}

		switch shellType {
		case "zsh":
			fmt.Println(color.CyanString("Detected Zsh."))
			appendConfigBlock(zshrc, zshConfig, "Zsh")
		case "bash":
			fmt.Println(color.CyanString("Detected Bash."))
			appendConfigBlock(bashrc, bashConfig, "Bash")
		default:
			// Fallback checks
			if _, err := os.Stat(zshrc); err == nil {
				fmt.Println("Found .zshrc, configuring Zsh...")
				appendConfigBlock(zshrc, zshConfig, "Zsh")
			} else if _, err := os.Stat(bashrc); err == nil {
				fmt.Println("Found .bashrc, configuring Bash...")
				appendConfigBlock(bashrc, bashConfig, "Bash")
			} else {
				fmt.Println("! Could not auto-detect shell config file.")
				fmt.Println("  Run " + color.New(color.Bold).Sprint("cwm setup --force") + " to choose manually.")
			}
		}
	},
}

func appendConfigBlock(filePath string, block string, shellName string) {
	// Create file and folders if missing
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf(color.RedString("Error creating folder %s: %v\n"), dir, err)
		return
	}

	contentBytes, _ := os.ReadFile(filePath)
	content := string(contentBytes)

	if strings.Contains(content, "# --- CWM History Setup ---") {
		fmt.Printf(color.GreenString("Success: ")+"%s is already configured in %s.\n", shellName, filepath.Base(filePath))
		return
	}

	fmt.Printf("Configuring %s...\n", shellName)
	fmt.Printf("  Target: %s\n", filePath)

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf(color.RedString("Error opening file: %v\n"), err)
		return
	}
	defer f.Close()

	if _, err := f.WriteString("\n" + strings.TrimSpace(block) + "\n"); err != nil {
		fmt.Printf(color.RedString("Error writing to file: %v\n"), err)
		return
	}

	// Update default database configs history_file path if empty
	database, errDb := db.InitDB()
	if errDb == nil {
		defer database.Close()
		currentHist, _ := db.GetConfigValue(database, "history_file")
		if currentHist == "" {
			var histFile string
			switch shellName {
			case "Zsh":
				home, _ := os.UserHomeDir()
				histFile = filepath.Join(home, ".zsh_history")
			case "Bash", "Git Bash":
				home, _ := os.UserHomeDir()
				histFile = filepath.Join(home, ".bash_history")
			}
			if histFile != "" {
				_ = db.SetConfigValue(database, "history_file", histFile)
			}
		}
	}

	fmt.Println(color.GreenString("Done! ") + "Please restart your " + shellName + " terminal.")
}

func setupPowershellHistory() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	docs := filepath.Join(home, "Documents")
	psPath := filepath.Join(docs, "PowerShell")
	legacyPath := filepath.Join(docs, "WindowsPowerShell")

	configured := false
	if _, err := os.Stat(psPath); err == nil {
		profile := filepath.Join(psPath, "Microsoft.PowerShell_profile.ps1")
		appendConfigBlock(profile, pwshConfig, "PowerShell (Core)")
		configured = true

		database, errDb := db.InitDB()
		if errDb == nil {
			defer database.Close()
			currentHist, _ := db.GetConfigValue(database, "history_file")
			if currentHist == "" {
				appData := os.Getenv("APPDATA")
				if appData != "" {
					histFile := filepath.Join(appData, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
					_ = db.SetConfigValue(database, "history_file", histFile)
				}
			}
		}
	}

	if _, err := os.Stat(legacyPath); err == nil {
		profile := filepath.Join(legacyPath, "Microsoft.PowerShell_profile.ps1")
		appendConfigBlock(profile, pwshConfig, "WindowsPowerShell (Legacy)")
		configured = true
	}

	if !configured {
		profile := filepath.Join(psPath, "Microsoft.PowerShell_profile.ps1")
		appendConfigBlock(profile, pwshConfig, "PowerShell")
	}
}

func init() {
	setupCmd.Flags().BoolVar(&forceSetupFlag, "force", false, "Force manual shell selection")
	rootCmd.AddCommand(setupCmd)
}
