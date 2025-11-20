package exec_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sonic-net/sonic-gnmi/pkg/exec"
)

func ExampleRunHostCommand() {
	// Execute a simple command on the host
	ctx := context.Background()
	result, err := exec.RunHostCommand(ctx, "hostname", nil, nil)
	if err != nil {
		log.Fatal(err)
	}

	if result.Error != nil {
		log.Printf("Command failed with exit code %d: %v", result.ExitCode, result.Error)
		log.Printf("Stderr: %s", result.Stderr)
	} else {
		fmt.Printf("Hostname: %s", result.Stdout)
	}
}

func ExampleRunHostCommand_withOptions() {
	// Execute a command with custom timeout and namespaces
	ctx := context.Background()
	opts := &exec.RunHostCommandOptions{
		Timeout:    10 * time.Second,
		Namespaces: []string{"pid", "net", "uts"},
	}

	result, err := exec.RunHostCommand(ctx, "ip", []string{"addr", "show"}, opts)
	if err != nil {
		log.Fatal(err)
	}

	if result.ExitCode == 0 {
		fmt.Printf("Network interfaces:\n%s", result.Stdout)
	}
}

func ExampleRunHostCommandSimple() {
	// Simple command execution when you only need stdout
	output, err := exec.RunHostCommandSimple("uname", "-a")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("System info: %s", output)
}

func ExampleParseCommand() {
	// Parse a command string into command and arguments
	cmd, args, err := exec.ParseCommand("docker ps -a --format json")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Command: %s\n", cmd)
	fmt.Printf("Arguments: %v\n", args)
	// Output:
	// Command: docker
	// Arguments: [ps -a --format json]
}
