package show_client

import (
	"encoding/json"
	log "github.com/golang/glog"
	"io/fs"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type zoneInfoDirStash struct {
	once            sync.Once
	cachedTimezones []string
	cachedError     error
}

var (
	zoneInfoDirPath = "/usr/share/zoneinfo"
	timezonesDirStash zoneInfoDirStash
)

func SetTimezonesDir(dirPath string) {
	zoneInfoDirPath = dirPath
}

func InvalidateTimezonesDirStash() {
	timezonesDirStash = zoneInfoDirStash{}
}

func getDate() ([]byte, error) {
	currentDate := time.Now().UTC().Format(time.UnixDate)
	dateResponse := map[string]interface{}{
		"date": currentDate,
	}
	return json.Marshal(dateResponse)
}

func getDateTimezone() ([]byte, error) {
	timezonesDirStash.once.Do(func() {
		timezonesDirStash.cachedTimezones, timezonesDirStash.cachedError = zoneInfoRunner(zoneInfoDirPath)
		if timezonesDirStash.cachedError != nil {
			log.Errorf("Unable to get list of timezones from %v, %v", zoneInfoDirPath, timezonesDirStash.cachedError)
			return
		}
	})
	if timezonesDirStash.cachedError != nil {
		return nil, timezonesDirStash.cachedError
	}
	timezonesResponse := map[string]interface{}{
		"timezones": timezonesDirStash.cachedTimezones,
	}
	return json.Marshal(timezonesResponse)
}

func zoneInfoRunner(dirpath string) ([]string, error) {
	var zones []string
	err := filepath.WalkDir(dirpath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case "posix", "right", "SystemV":
				return filepath.SkipDir
			}
			return nil
		}

		switch name {
		case "localtime", "zone.tab", "zone1970.tab",
			"iso3166.tab", "leapseconds", "leap-seconds.list",
			"tzdata.zi", "posixrules":
			return nil
		}

		relative_path, err := filepath.Rel(dirpath, path)
		if err != nil {
			return err
		}
		zones = append(zones, relative_path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(zones)
	return zones, nil
}
