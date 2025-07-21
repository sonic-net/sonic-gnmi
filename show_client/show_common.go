package show_client

import (
	"fmt"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const (
	dbIndex    = 0 // The first index for a query will be the DB
	tableIndex = 1 // The second index for a query will be the table

	minQueryLength = 2 // We need to support TARGET/TABLE as a minimum query
	maxQueryLength = 5 // We can support up to 5 elements in query (TARGET/TABLE/(2 KEYS)/FIELD)
)

func GetDataFromFile(fileName string) ([]byte, error) {
	fileContent, err := sdc.ImplIoutilReadFile(fileName)
	if err != nil {
		log.Errorf("Failed to read'%v', %v", fileName, err)
		return nil, err
	}
	log.V(4).Infof("getDataFromFile, output: %v", string(fileContent))
	return fileContent, nil
}

func GetDataFromTablePaths(tblPaths []sdc.TablePath) ([]byte, error) {
	msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		err := sdc.TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return sdc.Msi2Bytes(msi)
}

func CreateTablePathsFromQueries(queries [][]string) ([]sdc.TablePath, error) {
	var allPaths []sdc.TablePath

	// Create and validate gnmi path then create table path
	for _, q := range queries {
		queryLength := len(q)
		if queryLength < minQueryLength || queryLength > maxQueryLength {
			return nil, fmt.Errorf("invalid query %v: must support at least [DB, table] or at most [DB, table, key1, key2, field]", q)
		}

		// Build a gNMI path for validation:
		//   prefix = { Target: dbTarget }
		//   path   = { Elem: [ {Name:table}, {Name:key}, {Name:field} ] }

		dbTarget := q[dbIndex]
		prefix := &gnmipb.Path{Target: dbTarget}

		table := q[tableIndex]
		elems := []*gnmipb.PathElem{{Name: table}}

		// Additional elements like keys and fields
		for i := tableIndex + 1; i < queryLength; i++ {
			elems = append(elems, &gnmipb.PathElem{Name: q[i]})
		}

		path := &gnmipb.Path{Elem: elems}

		if tablePaths, err := sdc.PopulateTablePaths(prefix, path); err != nil {
			return nil, fmt.Errorf("query %v failed: %w", q, err)
		} else {
			allPaths = append(allPaths, tablePaths...)
		}
	}
	return allPaths, nil
}
