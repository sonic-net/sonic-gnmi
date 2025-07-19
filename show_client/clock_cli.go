package show_client

import (
	"encoding/json"
	"time"
)

func getDate() ([]byte, error) {
	currentDate := time.Now().UTC().Format(time.UnixDate)
	return json.Marshal(currentDate)
}
