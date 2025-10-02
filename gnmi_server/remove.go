package main

import (
    "os"
    "log"
)

func main() {
    err := os.Remove("/mnt/host/etc/sonic/config_db.json")
    if err != nil {
        log.Fatalf("Failed to remove file: %v", err)
    } else {
        log.Println("File removed successfully")
    }
}
