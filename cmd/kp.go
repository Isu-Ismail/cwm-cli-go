package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	forceKillFlag bool
)

type processInfo struct {
	PID      int
	Name     string
	CWD      string
	CmdLine  string
	IsSystem bool
}

var kpCmd = &cobra.Command{
	Use:     "kp <port>",
	Aliases: []string{"kill-port"},
	Short:   "Kill process holding a specific port",
	Long:    `Inspect and terminate processes listening on a target network port with interactive process visualization, CWD detection, and system process safeguards.`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		portStr := strings.TrimSpace(args[0])
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			fmt.Printf(color.RedString("Error: Invalid port number '%s'. Must be between 1 and 65535.\n"), portStr)
			os.Exit(1)
		}

		pids, err := findPIDsForPort(port)
		if err != nil || len(pids) == 0 {
			fmt.Println(color.GreenString("Port %d is already free.", port))
			return
		}

		for _, pid := range pids {
			proc := getProcessInfo(pid)

			if proc.IsSystem {
				fmt.Printf(color.RedString("Refusing to terminate critical system process %s (PID %d).\n"), proc.Name, proc.PID)
				continue
			}

			fmt.Println()
			fmt.Printf("%s", color.New(color.Bold, color.FgCyan).Sprintf("Found process holding port %d:\n", port))
			fmt.Printf("  • %-12s %d\n", color.New(color.Bold).Sprint("PID:"), proc.PID)
			if proc.Name != "" {
				fmt.Printf("  • %-12s %s\n", color.New(color.Bold).Sprint("Process:"), proc.Name)
			}
			if proc.CWD != "" {
				fmt.Printf("  • %-12s %s\n", color.New(color.Bold).Sprint("Directory:"), proc.CWD)
			}
			if proc.CmdLine != "" {
				fmt.Printf("  • %-12s %s\n", color.New(color.Bold).Sprint("Command:"), proc.CmdLine)
			}
			fmt.Println()

			if !forceKillFlag {
				fmt.Printf("Kill process on port %d? (y/N): ", port)
				reader := bufio.NewReader(os.Stdin)
				confirm, _ := reader.ReadString('\n')
				confirm = strings.ToLower(strings.TrimSpace(confirm))

				if confirm != "y" && confirm != "yes" {
					fmt.Println("Cancelled.")
					continue
				}
			}

			errKill := killProcess(proc.PID, forceKillFlag)
			if errKill != nil {
				fmt.Printf(color.RedString("Error killing process %d: %v\n"), proc.PID, errKill)
			} else {
				fmt.Printf(color.GreenString("✔ Process %d (%s) killed successfully.\n"), proc.PID, proc.Name)
			}
		}
	},
}

func findPIDsForPort(port int) ([]int, error) {
	pidMap := make(map[int]bool)

	if runtime.GOOS == "windows" {
		out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			targetPort := fmt.Sprintf(":%d", port)
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "LISTENING") && strings.Contains(line, targetPort) {
					fields := strings.Fields(line)
					if len(fields) >= 5 {
						localAddr := fields[1]
						if strings.HasSuffix(localAddr, targetPort) {
							if pid, errPid := strconv.Atoi(fields[len(fields)-1]); errPid == nil && pid > 0 {
								pidMap[pid] = true
							}
						}
					}
				}
			}
		}
	} else {
		out, err := exec.Command("lsof", "-i", fmt.Sprintf(":%d", port), "-sTCP:LISTEN", "-t").Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if pid, errPid := strconv.Atoi(l); errPid == nil && pid > 0 {
					pidMap[pid] = true
				}
			}
		} else {
			outSs, errSs := exec.Command("ss", "-tulpn").Output()
			if errSs == nil {
				lines := strings.Split(string(outSs), "\n")
				targetPort := fmt.Sprintf(":%d", port)
				for _, line := range lines {
					if strings.Contains(line, targetPort) && strings.Contains(line, "pid=") {
						parts := strings.Split(line, "pid=")
						if len(parts) > 1 {
							pidStr := strings.Split(parts[1], ",")[0]
							if pid, errPid := strconv.Atoi(pidStr); errPid == nil && pid > 0 {
								pidMap[pid] = true
							}
						}
					}
				}
			}
		}
	}

	var pids []int
	for pid := range pidMap {
		pids = append(pids, pid)
	}
	return pids, nil
}

func getProcessInfo(pid int) processInfo {
	proc := processInfo{
		PID:  pid,
		Name: fmt.Sprintf("PID %d", pid),
	}

	if isProtectedSystemPID(pid) {
		proc.IsSystem = true
		return proc
	}

	if runtime.GOOS == "windows" {
		psCmd := fmt.Sprintf("Get-CimInstance Win32_Process -Filter 'ProcessId = %d' | Select-Object Name, ExecutablePath, CommandLine | ConvertTo-Json", pid)
		out, err := exec.Command("powershell", "-NoProfile", "-Command", psCmd).Output()
		if err == nil && len(out) > 0 {
			var psData struct {
				Name           string `json:"Name"`
				ExecutablePath string `json:"ExecutablePath"`
				CommandLine    string `json:"CommandLine"`
			}
			if errJson := json.Unmarshal(out, &psData); errJson == nil {
				if psData.Name != "" {
					proc.Name = psData.Name
				}
				if psData.CommandLine != "" {
					proc.CmdLine = psData.CommandLine
				}
				if psData.ExecutablePath != "" {
					proc.CWD = filepath.Dir(psData.ExecutablePath)
				}
			}
		}
	} else {
		cwd, errCwd := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		if errCwd == nil {
			proc.CWD = cwd
		}

		cmdBytes, errCmd := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if errCmd == nil {
			parts := strings.Split(string(cmdBytes), "\x00")
			proc.CmdLine = strings.Join(parts, " ")
			if len(parts) > 0 && parts[0] != "" {
				proc.Name = filepath.Base(parts[0])
			}
		} else {
			outPs, errPs := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
			if errPs == nil {
				proc.Name = strings.TrimSpace(string(outPs))
			}
		}
	}

	if isProtectedSystemName(proc.Name) {
		proc.IsSystem = true
	}

	return proc
}

func isProtectedSystemPID(pid int) bool {
	return pid <= 4
}

func isProtectedSystemName(name string) bool {
	nameLower := strings.ToLower(name)
	protected := []string{
		"system", "system idle process", "csrss.exe", "lsass.exe",
		"services.exe", "smss.exe", "wininit.exe", "svchost.exe",
		"launchd", "systemd", "init", "kernel_task",
	}
	for _, p := range protected {
		if nameLower == p {
			return true
		}
	}
	return false
}

func killProcess(pid int, force bool) error {
	if runtime.GOOS == "windows" {
		args := []string{"/PID", strconv.Itoa(pid), "/T"}
		if force {
			args = append([]string{"/F"}, args...)
		}
		cmd := exec.Command("taskkill", args...)
		if err := cmd.Run(); err != nil && !force {
			cmdForce := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid), "/T")
			return cmdForce.Run()
		}
		return nil
	}

	sig := "-15"
	if force {
		sig = "-9"
	}
	cmd := exec.Command("kill", sig, strconv.Itoa(pid))
	err := cmd.Run()
	if err != nil && !force {
		cmdKill := exec.Command("kill", "-9", strconv.Itoa(pid))
		return cmdKill.Run()
	}
	return err
}

func init() {
	kpCmd.Flags().BoolVarP(&forceKillFlag, "force", "f", false, "Force kill process without confirmation prompt")
	rootCmd.AddCommand(kpCmd)
}
