package health

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/syslog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ContainerHealthInfo struct {
	ContainerID    string
	CPUUtilization float64
	MemoryUsage    float64
	DiskOccupation float64
	CertExpiration int64 // days until expiration
	Status         string
}

// GetHealthInfo gathers health information for the gNMI container
func GetHealthInfo() ([]ContainerHealthInfo, error) {
	// Here we interact with Docker to get container stats
	cmd := "docker stats --no-stream --format \"{{.Container}},{{.CPUPerc}},{{.MemPerc}},{{.Name}}\" | grep gnmi"
	args := strings.Fields(cmd)
	output, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve container stats: %v", err)
	}

	var healthInfo []ContainerHealthInfo
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}

		containerID := parts[0]
		container := ContainerHealthInfo{
			ContainerID:    containerID,
			CPUUtilization: parsePercentage(parts[1]),
			MemoryUsage:    parsePercentage(parts[2]),
			DiskOccupation: getDiskOccupation(containerID),
			CertExpiration: getCertExpiration(containerID),
			Status:         parts[3],
		}

		healthInfo = append(healthInfo, container)
	}

	return healthInfo, nil
}

// getDiskOccupation retrieves the disk usage for the container
func getDiskOccupation(containerID string) float64 {
	// Run the command to get disk usage inside the container
	cmd := fmt.Sprintf("docker exec %s df / | tail -1 | awk '{print $5}'", containerID)
	args := strings.Fields(cmd)
	output, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		fmt.Printf("failed to retrieve disk occupation for container %s: %v\n", containerID, err)
		return 0.0
	}
	return parsePercentage(strings.TrimSpace(string(output)))
}

// getCertExpiration retrieves the certificate expiration for the container
func getCertExpiration(containerID string) int64 {
	// Run the command to get the certificate from the container
	cmd := fmt.Sprintf("docker exec %s cat /path/to/cert.pem", containerID)
	args := strings.Fields(cmd)
	output, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		fmt.Printf("failed to retrieve certificate for container %s: %v\n", containerID, err)
		return 0
	}

	// Parse the certificate to get the expiration date
	block, _ := pem.Decode(output)
	if block == nil {
		fmt.Printf("failed to parse certificate PEM for container %s\n", containerID)
		return 0
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		fmt.Printf("failed to parse certificate for container %s: %v\n", containerID, err)
		return 0
	}

	// Calculate days until expiration
	return int64(time.Until(cert.NotAfter).Hours() / 24)
}

// LogHealthProofs logs container health information to syslog
func LogHealthProofs(container ContainerHealthInfo) {
	logwriter, err := syslog.New(syslog.LOG_NOTICE, "container_health")
	if err == nil {
		logwriter.Info("Health check for container " + container.ContainerID + ": " +
			"CPU=" + fmt.Sprintf("%.2f", container.CPUUtilization) +
			", Memory=" + fmt.Sprintf("%.2f", container.MemoryUsage) +
			", Disk=" + fmt.Sprintf("%.2f", container.DiskOccupation) +
			", CertExpiryDays=" + fmt.Sprintf("%d", container.CertExpiration))
	}
}

// Helper function to parse percentages
func parsePercentage(value string) float64 {
	value = strings.TrimSuffix(value, "%")
	parsedValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0.0
	}
	return parsedValue
}