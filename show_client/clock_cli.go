package show_client

import (
	"encoding/json"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"io/fs"
	"path/filepath"
	"sort"
	"time"
)

var (
	zoneInfoDirPath = "/usr/share/zoneinfo"
)

func SetTimezonesDir(dirPath string) {
	zoneInfoDirPath = dirPath
}

func getDate(prefix, path *gnmipb.Path) ([]byte, error) {
	currentDate := time.Now().UTC().Format(time.UnixDate)
	dateResponse := map[string]interface{}{
		"date": currentDate,
	}
	return json.Marshal(dateResponse)
}

func getDateTimezone(prefix, path *gnmipb.Path) ([]byte, error) {
	timezones, err := zoneInfoRunner(zoneInfoDirPath)
	if err != nil {
		log.Errorf("Unable to get list of timezones from %v, %v", zoneInfoDirPath, err)
		return nil, err
	}
	timezonesResponse := map[string]interface{}{
		"timezones": timezones,
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
