package gnmi

/*
import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	log "github.com/golang/glog"

	"github.com/go-redis/redis"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// virtual db is to Handle
// <1> data not in SONiC redis d
// <2> or different set of redis db data aggreggation
// <3> or non default TARGET_DEFINED stream subscription

// virtualDbTable stores the list of table path for the virtual table path
// May use callback function for virtaul table population
// Maybe create a new file for virtual table functions

var virtualDbTable map[TablePath][]TablePath  = {
	// virtual table for getting counters for all Ethernet ports
	{ TablePath{dbName:"COUNTERS_DB", tableName:"ETH_PORTS_STATS"}: []TablePath{}}
}

type V2R interface {
	virt2Real(TablePath) ([]TablePath, error)
}
type realPaths struct {
	populateFn func(TablePath) ([]TablePath, error)
	paths []TablePath
}



// Do special table key remapping for some db entries.
// Ex port name to oid in "COUNTERS_PORT_NAME_MAP" table of COUNTERS_DB
func isVirtualTable(virtTblPath TablePath) (bool, error) {
	tblPaths, ok := virtualDbTable[virtTblPath]
	if !ok {
		return false, nil
	}
	if len(tblPaths) != 0 {
		//Virtual table has been populated
		return true, nil
	}
	// populate all the paths under this virtual table
	return true, nil
}
*/
