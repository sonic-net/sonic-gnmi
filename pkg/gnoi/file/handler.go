package file

import (
    "os"
    "log"
    "strings"
    "path/filepath"
    "errors"
)

var allowedPrefixes = []string{"/tmp/", "/var/tmp/"}
var blacklistedFiles = []string{"/etc/sonic/config_db.json", "/etc/passwd"}

func isWhitelisted(path string) bool {
    for _, prefix := range allowedPrefixes {
        if strings.HasPrefix(path, prefix) {
            return true
        }
    }
    return false
}

func isBlacklisted(path string) bool {
    for _, b := range blacklistedFiles {
        if filepath.Clean(path) == b {
            return true
        }
    }
    return false
}

func hasPathTraversal(path string) bool {
    clean := filepath.Clean(path)
    // Ensures cleaned path does not escape allowed prefix
    for _, prefix := range allowedPrefixes {
        if strings.HasPrefix(clean, prefix) {
            return false
        }
    }
    return true
}

func RemoveFile(path string) error {
    log.Printf("Request to remove file: %s", path)
    if isBlacklisted(path) {
        log.Printf("Denied: blacklisted file removal attempt: %s", path)
        return errors.New("removal of critical system files is forbidden")
    }
    if !isWhitelisted(path) {
        log.Printf("Denied: file not in allowed directory: %s", path)
        return errors.New("only files in /tmp/ or /var/tmp/ can be removed")
    }
    if hasPathTraversal(path) {
        log.Printf("Denied: path traversal detected in: %s", path)
        return errors.New("path traversal detected")
    }
    err := os.Remove(path)
    if err != nil {
        log.Printf("Error removing file: %s: %v", path, err)
        return err
    }
    log.Printf("Successfully removed file: %s", path)
    return nil
}
