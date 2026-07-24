package cmd

import (
	"cwm/db"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	watchExcludeFlag string
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Record shell commands and manage hooks",
	Long:  `Inject or remove hooks in your shell profiles to automatically record executed commands.`,
}

var watchLogCmd = &cobra.Command{
	Use:   "log [command]",
	Short: "Log a command to history (Internal use)",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			return
		}
		commandVal := strings.TrimSpace(strings.Join(args, " "))
		if commandVal == "" {
			return
		}

		database, err := db.InitDB()
		if err != nil {
			return
		}
		// Get current working directory
		cwd, errCwd := os.Getwd()
		if errCwd != nil {
			cwd = "unknown"
		}

		// Check exclusions from config table
		excludeVal, errEx := db.GetConfigValue(database, "watch_exclude")
		if errEx == nil && excludeVal != "" {
			parts := strings.Split(excludeVal, ",")
			words := strings.Fields(commandVal)
			if len(words) > 0 {
				firstWord := strings.ToLower(words[0])
				cmdLower := strings.ToLower(commandVal)
				for _, p := range parts {
					target := strings.ToLower(strings.TrimSpace(p))
					if target != "" {
						if firstWord == target || strings.HasPrefix(cmdLower, target+" ") || cmdLower == target {
							return
						}
					}
				}
			}
		}

		// Insert log
		_, err = database.Exec("INSERT INTO history_logs (command, context_dir) VALUES (?, ?)", commandVal, cwd)
		if err != nil {
			return
		}

		// Sync to copy bank
		_ = db.SyncToCopyBank(database)
	},
}

const (
	hookStart = "# >>> CWM PROJECT HOOK START >>>"
	hookEnd   = "# <<< CWM PROJECT HOOK END <<<"
)

var watchStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Install the shell hook for command watch",
	Run: func(cmd *cobra.Command, args []string) {
		shellType := detectShell()
		if shellType == "" || (shellType != "powershell" && shellType != "pwsh" && shellType != "zsh" && shellType != "bash") {
			fmt.Println(color.RedString("cannot run watch: shell not supported yet"))
			os.Exit(1)
		}

		exePath, err := os.Executable()
		if err != nil {
			fmt.Printf(color.RedString("Error getting executable path: %v\n"), err)
			os.Exit(1)
		}
		exePath = filepath.ToSlash(exePath)

		profilePath, err := getProfilePath(shellType)
		if err != nil {
			fmt.Printf(color.RedString("Error resolving shell profile path: %v\n"), err)
			os.Exit(1)
		}

		// Generate hook content
		var hookContent string
		switch shellType {
		case "powershell", "pwsh":
			hookContent = fmt.Sprintf(`
# --- CWM Go Hook ---
$global:CWM_Last = ""
if (Get-Command prompt -ErrorAction SilentlyContinue) {
    if (-not (Get-Command CWM_Original_Prompt -ErrorAction SilentlyContinue)) {
        $origPrompt = Get-Command prompt
        New-Item -Path function:global:CWM_Original_Prompt -Value $origPrompt.ScriptBlock -Force > $null
    }
}
function global:prompt {
    $historyItem = Get-History -Count 1
    if ($historyItem) {
        $last = $historyItem.CommandLine
        if ($last -and ($last -ne $global:CWM_Last)) {
            $global:CWM_Last = $last
            & "%s" watch log $last > $null
        }
    }
    if (Get-Command CWM_Original_Prompt -ErrorAction SilentlyContinue) {
        CWM_Original_Prompt
    } else {
        "PS $((Get-Location).Path)> "
    }
}
`, exePath)
		case "zsh":
			hookContent = fmt.Sprintf(`
# --- CWM Go Hook ---
cwm_log_cmd() {
    local last
    last=$(fc -ln -1)
    if [[ "$last" != "$CWM_LAST" && -n "$last" ]]; then
        CWM_LAST="$last"
        "%s" watch log "$last" >/dev/null 2>&1 &
    fi
}
precmd_functions+=(cwm_log_cmd)
`, exePath)
		default: // bash
			hookContent = fmt.Sprintf(`
# --- CWM Go Hook ---
__cwm_log_cmd() {
    local last
    last=$(history 1 | sed -E 's/^ *[0-9]+ +//')
    if [[ "$last" != "$CWM_LAST" && -n "$last" ]]; then
        CWM_LAST="$last"
        "%s" watch log "$last" >/dev/null 2>&1 &
    fi
}
export PROMPT_COMMAND="__cwm_log_cmd; $PROMPT_COMMAND"
`, exePath)
		}

		// Ensure profile directory exists
		err = os.MkdirAll(filepath.Dir(profilePath), 0755)
		if err != nil {
			fmt.Printf(color.RedString("Error creating profile folder: %v\n"), err)
			os.Exit(1)
		}

		// Read existing profile content
		contentBytes, _ := os.ReadFile(profilePath)
		content := string(contentBytes)

		if strings.Contains(content, hookStart) {
			fmt.Println(color.YellowString("Watch hook is already installed in profile: ") + profilePath)
			return
		}

		injection := fmt.Sprintf("\n%s\n%s\n%s\n", hookStart, strings.TrimSpace(hookContent), hookEnd)
		newContent := content + injection

		err = os.WriteFile(profilePath, []byte(newContent), 0644)
		if err != nil {
			fmt.Printf(color.RedString("Error writing profile hook: %v\n"), err)
			os.Exit(1)
		}

		// Process -ex / --exclude / -e flags
		for i := 0; i < len(args); i++ {
			if args[i] == "-ex" || args[i] == "--exclude" || args[i] == "-e" {
				if i+1 < len(args) {
					watchExcludeFlag = args[i+1]
				}
			} else if strings.HasPrefix(args[i], "-ex=") || strings.HasPrefix(args[i], "--exclude=") || strings.HasPrefix(args[i], "-e=") {
				parts := strings.SplitN(args[i], "=", 2)
				watchExcludeFlag = parts[1]
			}
		}

		if watchExcludeFlag != "" {
			parts := strings.Split(watchExcludeFlag, ",")
			var cleanParts []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					cleanParts = append(cleanParts, p)
				}
			}
			cleanExclude := strings.Join(cleanParts, ",")
			if database, errDb := db.InitDB(); errDb == nil {
				_ = db.SetConfigValue(database, "watch_exclude", cleanExclude)
				database.Close()
			}
		}

		var reloadCmd string
		switch shellType {
		case "zsh":
			reloadCmd = "source ~/.zshrc"
		case "bash":
			reloadCmd = "source ~/.bashrc"
		default:
			reloadCmd = ". $PROFILE"
		}

		fmt.Println(color.GreenString("Watch session started successfully!"))
		fmt.Println("  Profile updated:   " + profilePath)
		if watchExcludeFlag != "" {
			fmt.Println("  Excluded Commands: " + color.CyanString(watchExcludeFlag))
		}
		if errClip := clipboard.WriteAll(reloadCmd); errClip == nil {
			fmt.Printf(color.GreenString("  Copied reload command '%s' to clipboard!\n"), reloadCmd)
			fmt.Println("  Press Ctrl+V (or right-click) and Enter to activate your profile immediately.")
		} else {
			fmt.Printf("  Run %s to activate your profile immediately.\n", color.CyanString(reloadCmd))
		}
	},
}

var watchStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Remove the shell hook and stop watch",
	Run: func(cmd *cobra.Command, args []string) {
		shellType := detectShell()
		if shellType == "" {
			fmt.Println(color.RedString("Error: Could not identify shell."))
			os.Exit(1)
		}

		profilePath, err := getProfilePath(shellType)
		if err != nil {
			fmt.Printf(color.RedString("Error getting profile: %v\n"), err)
			os.Exit(1)
		}

		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			fmt.Println(color.YellowString("No watch hook found (profile does not exist)."))
			return
		}

		contentBytes, err := os.ReadFile(profilePath)
		if err != nil {
			fmt.Printf(color.RedString("Error reading profile: %v\n"), err)
			os.Exit(1)
		}
		content := string(contentBytes)

		if !strings.Contains(content, hookStart) {
			fmt.Println(color.YellowString("No active CWM watch hook detected in profile."))
			return
		}

		// Regex to remove block
		re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(hookStart) + `.*?` + regexp.QuoteMeta(hookEnd) + `\s*`)
		newContent := re.ReplaceAllString(content, "")

		err = os.WriteFile(profilePath, []byte(newContent), 0644)
		if err != nil {
			fmt.Printf(color.RedString("Error updating profile: %v\n"), err)
			os.Exit(1)
		}

		var reloadCmd string
		switch shellType {
		case "zsh":
			reloadCmd = "source ~/.zshrc"
		case "bash":
			reloadCmd = "source ~/.bashrc"
		default:
			reloadCmd = ". $PROFILE"
		}

		fmt.Println(color.GreenString("CWM watch hook removed from profile successfully."))
		if errClip := clipboard.WriteAll(reloadCmd); errClip == nil {
			fmt.Printf(color.GreenString("  Copied reload command '%s' to clipboard!\n"), reloadCmd)
			fmt.Println("  Press Ctrl+V (or right-click) and Enter to apply changes immediately.")
		} else {
			fmt.Printf("  Run %s to apply changes immediately.\n", color.CyanString(reloadCmd))
		}
	},
}

var watchStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show watch status",
	Run: func(cmd *cobra.Command, args []string) {
		shellType := detectShell()
		if shellType == "" {
			fmt.Println("Watch status: INACTIVE (Shell undetected)")
			return
		}

		profilePath, err := getProfilePath(shellType)
		if err != nil {
			fmt.Println("Watch status: INACTIVE (Profile error)")
			return
		}

		contentBytes, _ := os.ReadFile(profilePath)
		if strings.Contains(string(contentBytes), hookStart) {
			fmt.Println(color.GreenString("Watch status: ACTIVE"))
			fmt.Printf("Shell Type:        %s\n", shellType)
			fmt.Printf("Profile File:      %s\n", profilePath)
			if database, errDb := db.InitDB(); errDb == nil {
				exVal, _ := db.GetConfigValue(database, "watch_exclude")
				database.Close()
				if exVal != "" {
					fmt.Printf("Excluded Commands: %s\n", color.CyanString(exVal))
				}
			}
		} else {
			fmt.Println("Watch status: INACTIVE")
		}
	},
}

// detectShell traverses parent tree
func detectShell() string {
	shellEnv := strings.ToLower(os.Getenv("SHELL"))
	if strings.Contains(shellEnv, "pwsh") {
		return "pwsh"
	}
	if strings.Contains(shellEnv, "powershell") {
		return "powershell"
	}
	if strings.Contains(shellEnv, "zsh") {
		return "zsh"
	}
	if strings.Contains(shellEnv, "bash") {
		return "bash"
	}

	if runtime.GOOS == "windows" {
		if os.Getenv("PSModulePath") != "" {
			_, err := exec.LookPath("pwsh")
			if err == nil {
				return "pwsh"
			}
			return "powershell"
		}
		return "cmd"
	}

	return "bash" // Default fallback
}

func getProfilePath(shellType string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if shellType == "pwsh" {
		if runtime.GOOS == "windows" {
			out, err := exec.Command("pwsh", "-NoProfile", "-Command", "Write-Host $PROFILE.CurrentUserCurrentHost").Output()
			if err == nil {
				return strings.TrimSpace(string(out)), nil
			}
		}
		return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"), nil
	}

	if shellType == "powershell" {
		if runtime.GOOS == "windows" {
			out, err := exec.Command("powershell", "-NoProfile", "-Command", "Write-Host $PROFILE.CurrentUserCurrentHost").Output()
			if err == nil {
				return strings.TrimSpace(string(out)), nil
			}
		}
		return filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"), nil
	}

	if shellType == "zsh" {
		return filepath.Join(home, ".zshrc"), nil
	}

	// bash
	bashProfile := filepath.Join(home, ".bash_profile")
	if _, err := os.Stat(bashProfile); err == nil {
		return bashProfile, nil
	}
	return filepath.Join(home, ".bashrc"), nil
}

func init() {
	watchStartCmd.Flags().StringVarP(&watchExcludeFlag, "exclude", "e", "", "Exclude commands from watch logging (e.g. -ex cwm,python,clear)")

	watchCmd.AddCommand(watchLogCmd)
	watchCmd.AddCommand(watchStartCmd)
	watchCmd.AddCommand(watchStopCmd)
	watchCmd.AddCommand(watchStatusCmd)
	rootCmd.AddCommand(watchCmd)
}
