package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/show"
)

func main() {
	// Handle help flag specially
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	if len(os.Args) < 2 {
		fmt.Printf("test-show - SONiC Show Command Test Utility\n\n")
		fmt.Printf("This tool tests the show command CLI wrapper functionality.\n")
		fmt.Printf("It provides a safe way to test show commands.\n\n")
		showUsage()
		os.Exit(1)
	}

	ss := show.NewSonicShow()
	command := os.Args[1]

	switch command {
	case "dpu-status":
		fmt.Println("=== Testing show chassis modules midplane-status command ===")

		var dpuName string
		if len(os.Args) > 2 {
			dpuName = os.Args[2]

			// Validate DPU name format
			if err := show.ValidateDPUName(dpuName); err != nil {
				fmt.Printf("Invalid DPU name: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Checking status for specific DPU: %s\n", dpuName)
			result, err := ss.GetSpecificDPUStatus(dpuName)
			if err != nil {
				fmt.Printf("Error getting DPU status: %v\n", err)
				fmt.Println("\nThis is expected if show command is not available or DPU doesn't exist.")
				os.Exit(1)
			}

			displayResult(result, fmt.Sprintf("DPU %s Status", dpuName))
		} else {
			fmt.Println("Getting status for all DPUs")
			result, err := ss.GetAllDPUStatus()
			if err != nil {
				fmt.Printf("Error getting all DPU status: %v\n", err)
				fmt.Println("\nThis is expected if show command is not available.")
				os.Exit(1)
			}

			displayResult(result, "All DPUs Status")
		}

	case "dpu-reachable":
		if len(os.Args) < 3 {
			fmt.Println("Error: dpu-reachable requires a DPU name")
			fmt.Println("Usage: test-show dpu-reachable <DPU-name>")
			os.Exit(1)
		}

		dpuName := os.Args[2]

		// Validate DPU name format
		if err := show.ValidateDPUName(dpuName); err != nil {
			fmt.Printf("Invalid DPU name: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("=== Testing DPU reachability check for %s ===\n", dpuName)

		reachable, err := ss.IsDPUReachable(dpuName)
		if err != nil {
			fmt.Printf("Error checking DPU reachability: %v\n", err)
			fmt.Println("\nThis is expected if show command is not available or DPU doesn't exist.")
			os.Exit(1)
		}

		fmt.Printf("DPU %s is reachable: %t\n", dpuName, reachable)

		if reachable {
			fmt.Printf("✓ DPU %s is reachable\n", dpuName)
		} else {
			fmt.Printf("✗ DPU %s is NOT reachable\n", dpuName)
		}

	case "validate-dpu":
		if len(os.Args) < 3 {
			fmt.Println("Error: validate-dpu requires a DPU name")
			fmt.Println("Usage: test-show validate-dpu <DPU-name>")
			os.Exit(1)
		}

		dpuName := os.Args[2]
		fmt.Printf("=== Testing DPU name validation for '%s' ===\n", dpuName)

		err := show.ValidateDPUName(dpuName)
		if err != nil {
			fmt.Printf("✗ Invalid DPU name: %v\n", err)
		} else {
			fmt.Printf("✓ Valid DPU name: %s\n", dpuName)
		}

	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Printf("Use 'test-show help' to see available commands.\n")
		os.Exit(1)
	}
}

func displayResult(result *show.ChassisModulesResult, title string) {
	fmt.Printf("\n=== %s ===\n", title)

	if result.HasError {
		fmt.Printf("Error occurred: %s\n", result.Message)
		return
	}

	if len(result.DPUs) == 0 {
		fmt.Printf("No DPUs found.\n")
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
		return
	}

	fmt.Printf("Found %d DPU(s):\n", len(result.DPUs))
	fmt.Printf("%-8s %-15s %-12s\n", "Name", "IP Address", "Reachability")
	fmt.Printf("%-8s %-15s %-12s\n", "----", "----------", "------------")

	for _, dpu := range result.DPUs {
		fmt.Printf("%-8s %-15s %-12s\n", dpu.Name, dpu.IPAddress, dpu.Reachability)
	}

	// Also show JSON output
	fmt.Println("\n=== JSON Output ===")
	jsonData, _ := json.MarshalIndent(result, "", "  ")
	fmt.Printf("%s\n", jsonData)
}

func showUsage() {
	fmt.Printf("Usage: test-show <command> [args...]\n\n")
	fmt.Printf("Commands:\n")
	fmt.Printf("  dpu-status [DPU-name]   - Show DPU status (all DPUs or specific DPU)\n")
	fmt.Printf("  dpu-reachable <DPU-name> - Check if specific DPU is reachable\n")
	fmt.Printf("  validate-dpu <DPU-name>  - Validate DPU name format\n")
	fmt.Printf("  help                     - Show detailed help\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  test-show dpu-status        # Show all DPUs\n")
	fmt.Printf("  test-show dpu-status DPU0   # Show status for DPU0\n")
	fmt.Printf("  test-show dpu-reachable DPU3 # Check if DPU3 is reachable\n")
	fmt.Printf("  test-show validate-dpu DPU0  # Validate DPU0 name format\n\n")
}

func showHelp() {
	fmt.Printf("test-show - SONiC Show Command Test Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility validates the show command CLI wrapper functionality.\n")
	fmt.Printf("  It provides a way to test the show package integration with the\n")
	fmt.Printf("  SONiC show command-line tool. The utility demonstrates:\n")
	fmt.Printf("  - Calling show chassis modules commands through the wrapper\n")
	fmt.Printf("  - Parsing command output and error handling\n")
	fmt.Printf("  - Converting results to structured data formats\n")
	fmt.Printf("  - DPU status checking and reachability validation\n\n")
	showUsage()
	fmt.Printf("SAFETY NOTES:\n")
	fmt.Printf("  - All commands are read-only and safe to use\n")
	fmt.Printf("  - No system modifications are performed\n")
	fmt.Printf("  - Only queries existing DPU status information\n\n")
	fmt.Printf("PURPOSE:\n")
	fmt.Printf("  This tool is for testing and validation purposes.\n")
	fmt.Printf("  Use this to verify that the show command wrapper works\n")
	fmt.Printf("  correctly with your system's show command installation.\n\n")
	fmt.Printf("DPU NAME FORMAT:\n")
	fmt.Printf("  DPU names should follow the format DPU<number> (e.g., DPU0, DPU1, DPU3)\n")
	fmt.Printf("  Case-sensitive and must start with 'DPU' followed by digits.\n\n")
}
