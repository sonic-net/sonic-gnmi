package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/diskspace"
)

func main() {
	// Handle help flag specially
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	// Handle cleanup commands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "cleanup":
			runCleanup()
			return
		case "estimate":
			runEstimate()
			return
		}
	}

	fmt.Printf("diskspace - Disk Space Utility\n\n")
	fmt.Printf("This tool shows total and free disk space in MB.\n\n")

	// Test root filesystem
	fmt.Printf("=== Root Filesystem (/) ===\n")
	testPath("/")

	// Test /host if it exists
	if _, err := os.Stat("/host"); err == nil {
		fmt.Printf("\n=== Host Filesystem (/host) ===\n")
		testPath("/host")
	}

	// Test /tmp
	fmt.Printf("\n=== Temp Filesystem (/tmp) ===\n")
	testPath("/tmp")

	// Show cleanup options
	fmt.Printf("\n=== Cleanup Options ===\n")
	showCleanupInfo()
}

func testPath(path string) {
	fmt.Printf("Path: %s\n", path)

	total, err := diskspace.GetDiskTotalSpaceMB(path)
	if err != nil {
		fmt.Printf("ERROR getting total space: %v\n", err)
		return
	}

	free, err := diskspace.GetDiskFreeSpaceMB(path)
	if err != nil {
		fmt.Printf("ERROR getting free space: %v\n", err)
		return
	}

	used := total - free
	usedPercent := float64(used) / float64(total) * 100

	fmt.Printf("Total space: %d MB\n", total)
	fmt.Printf("Free space:  %d MB\n", free)
	fmt.Printf("Used space:  %d MB (%.1f%%)\n", used, usedPercent)

	// Visual bar
	fmt.Printf("Usage bar:   ")
	printUsageBar(usedPercent)
	fmt.Println()
}

func printUsageBar(usedPercent float64) {
	const barWidth = 40
	used := int(usedPercent * barWidth / 100)

	fmt.Print("[")
	for i := 0; i < barWidth; i++ {
		if i < used {
			if usedPercent > 90 {
				fmt.Print("█") // Critical - solid block
			} else if usedPercent > 80 {
				fmt.Print("▓") // Warning - medium block
			} else {
				fmt.Print("▒") // Normal - light block
			}
		} else {
			fmt.Print("░") // Free space - very light block
		}
	}
	fmt.Printf("] %.1f%%", usedPercent)
}

func runEstimate() {
	fmt.Printf("diskspace - Cleanup Estimation\n\n")

	estimate, err := diskspace.GetCleanupSpaceMB()
	if err != nil {
		fmt.Printf("ERROR estimating cleanup space: %v\n", err)
		return
	}

	fmt.Printf("Estimated space that can be freed: %d MB\n\n", estimate)

	// Show details for each cleanup area
	fmt.Printf("Breakdown:\n")

	coreResult, err := diskspace.CleanupCoreFiles(diskspace.CleanupOptions{DryRun: true})
	if err != nil {
		fmt.Printf("  Core files (/var/core): ERROR - %v\n", err)
	} else {
		fmt.Printf("  Core files (/var/core): %d MB (%d items)\n", coreResult.SpaceFreedMB, coreResult.FilesRemoved)
	}

	dumpResult, err := diskspace.CleanupDumpFiles(diskspace.CleanupOptions{DryRun: true})
	if err != nil {
		fmt.Printf("  Dump files (/var/dump): ERROR - %v\n", err)
	} else {
		fmt.Printf("  Dump files (/var/dump): %d MB (%d items)\n", dumpResult.SpaceFreedMB, dumpResult.FilesRemoved)
	}
}

func runCleanup() {
	fmt.Printf("diskspace - Disk Space Cleanup\n\n")

	// First show current usage
	fmt.Printf("=== Current Disk Usage ===\n")
	if _, err := os.Stat("/host"); err == nil {
		testPath("/host")
	} else {
		testPath("/")
	}

	// Show what would be cleaned
	fmt.Printf("\n=== Cleanup Estimation ===\n")
	estimate, err := diskspace.GetCleanupSpaceMB()
	if err != nil {
		fmt.Printf("ERROR estimating cleanup space: %v\n", err)
		return
	}

	if estimate == 0 {
		fmt.Printf("No files found to clean up.\n")
		return
	}

	fmt.Printf("Total space that can be freed: %d MB\n\n", estimate)

	// Confirm before cleanup
	fmt.Printf("⚠️  WARNING: This will permanently delete files!\n")
	fmt.Printf("Do you want to proceed with cleanup? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("ERROR reading response: %v\n", err)
		return
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response != "yes" && response != "y" {
		fmt.Printf("Cleanup cancelled.\n")
		return
	}

	// Perform cleanup
	fmt.Printf("\n=== Performing Cleanup ===\n")

	totalFreed := int64(0)
	totalFiles := 0

	// Cleanup core files
	fmt.Printf("Cleaning core files...\n")
	coreResult, err := diskspace.CleanupCoreFiles(diskspace.CleanupOptions{DryRun: false})
	if err != nil {
		fmt.Printf("ERROR cleaning core files: %v\n", err)
	} else {
		fmt.Printf("  Freed %d MB by removing %d items from /var/core\n", coreResult.SpaceFreedMB, coreResult.FilesRemoved)
		totalFreed += coreResult.SpaceFreedMB
		totalFiles += coreResult.FilesRemoved
		if len(coreResult.Errors) > 0 {
			fmt.Printf("  %d errors occurred during core cleanup\n", len(coreResult.Errors))
		}
	}

	// Cleanup dump files
	fmt.Printf("Cleaning dump files...\n")
	dumpResult, err := diskspace.CleanupDumpFiles(diskspace.CleanupOptions{DryRun: false})
	if err != nil {
		fmt.Printf("ERROR cleaning dump files: %v\n", err)
	} else {
		fmt.Printf("  Freed %d MB by removing %d items from /var/dump\n", dumpResult.SpaceFreedMB, dumpResult.FilesRemoved)
		totalFreed += dumpResult.SpaceFreedMB
		totalFiles += dumpResult.FilesRemoved
		if len(dumpResult.Errors) > 0 {
			fmt.Printf("  %d errors occurred during dump cleanup\n", len(dumpResult.Errors))
		}
	}

	fmt.Printf("\n=== Cleanup Complete ===\n")
	fmt.Printf("Total space freed: %d MB\n", totalFreed)
	fmt.Printf("Total files removed: %d\n", totalFiles)

	// Show updated usage
	fmt.Printf("\n=== Updated Disk Usage ===\n")
	if _, err := os.Stat("/host"); err == nil {
		testPath("/host")
	} else {
		testPath("/")
	}
}

func showCleanupInfo() {
	estimate, err := diskspace.GetCleanupSpaceMB()
	if err != nil {
		fmt.Printf("Could not estimate cleanup space: %v\n", err)
		return
	}

	if estimate > 0 {
		fmt.Printf("💡 %d MB can be freed through cleanup\n", estimate)
		fmt.Printf("   Run 'diskspace estimate' for details\n")
		fmt.Printf("   Run 'diskspace cleanup' to perform cleanup\n")
	} else {
		fmt.Printf("✓ No cleanup needed - directories are already clean\n")
	}
}

func showHelp() {
	fmt.Printf("diskspace - Disk Space Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility shows disk space information and cleanup options.\n")
	fmt.Printf("  It provides total and free space for various filesystems.\n\n")
	fmt.Printf("USAGE:\n")
	fmt.Printf("  diskspace              Show disk space information\n")
	fmt.Printf("  diskspace estimate     Show cleanup space estimation\n")
	fmt.Printf("  diskspace cleanup      Perform interactive cleanup\n")
	fmt.Printf("  diskspace help         Show this help message\n\n")
	fmt.Printf("CLEANUP OPERATIONS:\n")
	fmt.Printf("  • Removes core dump files from /var/core/*\n")
	fmt.Printf("  • Removes dump files from /var/dump/*\n")
	fmt.Printf("  • Shows space estimation before cleanup\n")
	fmt.Printf("  • Requires confirmation before deletion\n\n")
	fmt.Printf("OUTPUT:\n")
	fmt.Printf("  • Total space in MB\n")
	fmt.Printf("  • Free space in MB\n")
	fmt.Printf("  • Used space in MB and percentage\n")
	fmt.Printf("  • Visual usage bar with color coding\n")
	fmt.Printf("  • Cleanup recommendations\n\n")
	fmt.Printf("FILESYSTEMS CHECKED:\n")
	fmt.Printf("  • / (root filesystem)\n")
	fmt.Printf("  • /host (if exists)\n")
	fmt.Printf("  • /tmp (temporary filesystem)\n\n")
	fmt.Printf("SAFETY:\n")
	fmt.Printf("  • Dry-run mode for estimation\n")
	fmt.Printf("  • Interactive confirmation required\n")
	fmt.Printf("  • Detailed logging and error reporting\n\n")
}
