package cmd

import (
	"bufio"
	"cwm/db"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var forceSetupFlag bool

const WrapperVersionHeader = "# --- CWM Shell Wrapper v1.0.0 (build:20260723) ---"
const WrapperVersionFooter = "# --- End CWM Shell Wrapper ---"

const (
	bashConfig = `
# --- CWM Shell Wrapper v1.0.0 (build:20260723) ---
shopt -s histappend
export PROMPT_COMMAND="history -a; $PROMPT_COMMAND"
export HISTCONTROL=ignoreboth

# Native CWM Execution Shell Function
cwm() {
    if [ "$1" = "exec" ] || [[ " $* " =~ " -x " ]] || [[ " $* " =~ " --exec " ]]; then
        local clean_args=()
        for arg in "$@"; do
            if [ "$arg" != "-x" ] && [ "$arg" != "--exec" ] && [ "$arg" != "exec" ] && [ "$arg" != "get" ]; then
                clean_args+=("$arg")
            fi
        done
        if [ ${#clean_args[@]} -eq 0 ]; then
            command cwm "$@"
        else
            local cmd
            cmd=$(command cwm get --show "${clean_args[@]}")
            if [ -n "$cmd" ]; then
                eval "$cmd"
            fi
        fi
    else
        command cwm "$@"
    fi
}
# --- End CWM Shell Wrapper ---
`
	zshConfig = `
# --- CWM Shell Wrapper v1.0.0 (build:20260723) ---
HISTFILE="$HOME/.zsh_history"
HISTSIZE=50000
SAVEHIST=50000
setopt INC_APPEND_HISTORY
setopt HIST_IGNORE_DUPS
setopt HIST_IGNORE_ALL_DUPS
setopt HIST_IGNORE_SPACE
unsetopt EXTENDED_HISTORY
setopt NO_EXTENDED_HISTORY

# Native CWM Execution Shell Function
cwm() {
    if [ "$1" = "exec" ] || [[ " $* " =~ " -x " ]] || [[ " $* " =~ " --exec " ]]; then
        local clean_args=()
        for arg in "$@"; do
            if [ "$arg" != "-x" ] && [ "$arg" != "--exec" ] && [ "$arg" != "exec" ] && [ "$arg" != "get" ]; then
                clean_args+=("$arg")
            fi
        done
        if [ ${#clean_args[@]} -eq 0 ]; then
            command cwm "$@"
        else
            local cmd
            cmd=$(command cwm get --show "${clean_args[@]}")
            if [ -n "$cmd" ]; then
                eval "$cmd"
            fi
        fi
    else
        command cwm "$@"
    fi
}
# --- End CWM Shell Wrapper ---
`
	pwshConfig = `
# --- CWM Shell Wrapper v1.0.0 (build:20260723) ---
Set-PSReadLineOption -HistorySaveStyle SaveIncrementally
Set-PSReadLineOption -HistoryNoDuplicates

# Native CWM Execution Shell Function
function cwm {
    if ($args.Count -gt 0 -and ($args -contains '-x' -or $args -contains '--exec' -or $args[0] -eq 'exec')) {
        $cleanArgs = @()
        foreach ($a in $args) {
            if ($a -ne '-x' -and $a -ne '--exec' -and $a -ne 'exec' -and $a -ne 'get') {
                $cleanArgs += $a
            }
        }
        if ($cleanArgs.Count -eq 0) {
            & cwm.exe @args
        } else {
            $cmd = & cwm.exe get --show @cleanArgs
            if ($cmd) {
                Invoke-Expression $cmd
            }
        }
    } else {
        & cwm.exe @args
    }
}
# --- End CWM Shell Wrapper ---
`
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure shell for history synchronization and native execution",
	Long:  `Configures your shell (Bash, Zsh, or PowerShell) profile for instant history synchronization and native shell execution (cd, env vars).`,
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
				updateOrAppendCwmBlock(bashrc, bashConfig, "Bash")
			case "2":
				updateOrAppendCwmBlock(zshrc, zshConfig, "Zsh")
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
				updateOrAppendCwmBlock(bashrc, bashConfig, "Git Bash")
			} else {
				fmt.Println(color.CyanString("Detected Windows System."))
				setupPowershellHistory()
			}
			return
		}

		switch shellType {
		case "zsh":
			fmt.Println(color.CyanString("Detected Zsh."))
			updateOrAppendCwmBlock(zshrc, zshConfig, "Zsh")
		case "bash":
			fmt.Println(color.CyanString("Detected Bash."))
			updateOrAppendCwmBlock(bashrc, bashConfig, "Bash")
		default:
			if _, err := os.Stat(zshrc); err == nil {
				fmt.Println("Found .zshrc, configuring Zsh...")
				updateOrAppendCwmBlock(zshrc, zshConfig, "Zsh")
			} else if _, err := os.Stat(bashrc); err == nil {
				fmt.Println("Found .bashrc, configuring Bash...")
				updateOrAppendCwmBlock(bashrc, bashConfig, "Bash")
			} else {
				fmt.Println("! Could not auto-detect shell config file.")
				fmt.Println("  Run " + color.New(color.Bold).Sprint("cwm setup --force") + " to choose manually.")
			}
		}
	},
}

func updateOrAppendCwmBlock(filePath string, newBlock string, shellName string) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf(color.RedString("Error creating folder %s: %v\n"), dir, err)
		return
	}

	contentBytes, err := os.ReadFile(filePath)
	content := ""
	if err == nil {
		content = string(contentBytes)
	}

	// Check if already up to date with exact Header
	if strings.Contains(content, WrapperVersionHeader) {
		fmt.Printf(color.GreenString("Success: ")+"%s profile is up to date in %s.\n", shellName, filepath.Base(filePath))
		return
	}

	lines := strings.Split(content, "\n")
	var newLines []string
	inCwmBlock := false
	replacedBlock := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "# --- CWM Shell Wrapper") || strings.HasPrefix(trimmed, "# --- CWM") {
			inCwmBlock = true
			if !replacedBlock {
				newLines = append(newLines, strings.TrimSpace(newBlock))
				replacedBlock = true
			}
			continue
		}

		if strings.HasPrefix(trimmed, "# --- End CWM Shell Wrapper") || strings.HasPrefix(trimmed, "# --- End CWM") {
			inCwmBlock = false
			continue
		}

		if inCwmBlock {
			continue
		}

		// Keep ALL non-CWM lines untouched (Oh My Posh, starship, custom aliases, etc.)
		newLines = append(newLines, line)
	}

	finalContent := strings.Join(newLines, "\n")
	if !replacedBlock {
		if strings.TrimSpace(finalContent) != "" && !strings.HasSuffix(finalContent, "\n") {
			finalContent += "\n\n"
		} else if strings.TrimSpace(finalContent) != "" {
			finalContent += "\n"
		}
		finalContent += strings.TrimSpace(newBlock) + "\n"
	}

	errWrite := os.WriteFile(filePath, []byte(finalContent), 0644)
	if errWrite != nil {
		fmt.Printf(color.RedString("Error writing profile %s: %v\n"), filePath, errWrite)
		return
	}

	fmt.Printf(color.GreenString("Configured %s:\n"), shellName)
	fmt.Printf("  Target: %s\n", filePath)

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

	if shellName == "Bash" || shellName == "Git Bash" || shellName == "Zsh" {
		reloadCmd := "source ~/.bashrc"
		if shellName == "Zsh" {
			reloadCmd = "source ~/.zshrc"
		}
		if errClip := clipboard.WriteAll(reloadCmd); errClip == nil {
			fmt.Printf(color.GreenString("Copied reload command '%s' to clipboard!\n"), reloadCmd)
			fmt.Println("Paste (Ctrl+V) and press Enter to activate your profile immediately.")
		}
	}
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
		updateOrAppendCwmBlock(profile, pwshConfig, "PowerShell (Core)")
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
		updateOrAppendCwmBlock(profile, pwshConfig, "WindowsPowerShell (Legacy)")
		configured = true
	}

	if !configured {
		profile := filepath.Join(psPath, "Microsoft.PowerShell_profile.ps1")
		updateOrAppendCwmBlock(profile, pwshConfig, "PowerShell")
	}

	reloadCmd := ". $PROFILE"
	if errClip := clipboard.WriteAll(reloadCmd); errClip == nil {
		fmt.Printf(color.GreenString("Copied reload command '%s' to clipboard!\n"), reloadCmd)
		fmt.Println("Paste (Ctrl+V) and press Enter to activate your profile immediately.")
	} else {
		fmt.Println(color.GreenString("Done! ") + "Please run: " + color.CyanString(reloadCmd))
	}
}

func init() {
	setupCmd.Flags().BoolVar(&forceSetupFlag, "force", false, "Force manual shell selection")
	rootCmd.AddCommand(setupCmd)
}
