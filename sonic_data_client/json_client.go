package client

import (
	"os"
	"fmt"
	"strconv"
	"strings"
	"io/ioutil"
	"encoding/json"

	log "github.com/golang/glog"
)

type JsonClient struct {
	jsonData map[string]interface{}
}

func DecodeJsonTable(database map[string]interface{}, tableName string) (map[string]interface{}, error) {
	vtable, ok := database[tableName]
	if !ok {
		log.V(2).Infof("Invalid database %v -> %v", tableName, database)
		return nil, fmt.Errorf("Invalid database %v -> %v", tableName, database)
	}
	v, ok := vtable.(map[string]interface{})
	if !ok {
		log.V(2).Infof("Invalid table %v", vtable)
		return nil, fmt.Errorf("Invalid table %v", vtable)
	}
	return v, nil
}

func DecodeJsonEntry(table map[string]interface{}, entryName string) (map[string]interface{}, error) {
	ventry, ok := table[entryName]
	if !ok {
		log.V(2).Infof("Invalid entry %v", table)
		return nil, fmt.Errorf("Invalid entry %v", table)
	}
	v, ok := ventry.(map[string]interface{})
	if !ok {
		log.V(2).Infof("Invalid entry %v", ventry)
		return nil, fmt.Errorf("Invalid entry %v", ventry)
	}
	return v, nil
}

func DecodeJsonField(entry map[string]interface{}, fieldName string) (*string, []interface{}, error) {
	vfield, ok := entry[fieldName]
	if !ok {
		log.V(2).Infof("Invalid entry %v", entry)
		return nil, nil, fmt.Errorf("Invalid entry %v", entry)
	}
	str, ok := vfield.(string)
	if ok {
		return &str, nil, nil
	}
	list, ok := vfield.([]interface{})
	if ok {
		return nil, list, nil
	}
	return nil, nil, fmt.Errorf("Invalid field %v", vfield)
}

func DecodeJsonListItem(list []interface{}, index string) (*string, error) {
	id, err := strconv.Atoi(index)
	if err != nil {
		log.V(2).Infof("Invalid index %v", index)
		return nil, fmt.Errorf("Invalid index %v", index)
	}
	if id < 0 || id >= len(list) {
		log.V(2).Infof("Invalid index %v", index)
		return nil, fmt.Errorf("Invalid index %v", index)
	}
	vitem := list[id]
	str, ok := vitem.(string)
	if ok {
		return &str, nil
	}
	return nil, fmt.Errorf("Invalid item %v", vitem)
}

func NewJsonClient(fileName string) (*JsonClient, error) {
	var client JsonClient

	jsonFile, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()
 
	jsonData, err := ioutil.ReadAll(jsonFile)
	if err!= nil {
		return nil, err
	}
	res, err := parseJson([]byte(jsonData))
	if err != nil {
		return nil, err
	}
	var ok bool
	client.jsonData, ok = res.(map[string]interface{})
	if !ok {
		log.V(2).Infof("Invalid checkpoint %v", fileName)
		return nil, fmt.Errorf("Invalid checkpoint %v", fileName)
	}

	return &client, nil
}

func FixPath(path []string) (ret []string){
	// Jsonpatch uses "~1" to support "/" in path
	// Replace "~1" to compare json data 
	if len(path) >= 2 {
		path[1] = strings.ReplaceAll(path[1], "~1", "/")
	}
	return path
}

func (c *JsonClient) Get(path []string) ([]byte, error) {
	// The expect real db path could be in one of the formats:
	// <1> DB Table
	// <2> DB Table Key
	// <3> DB Table Key Field
	// <4> DB Table Key Field Index
	jv := []byte{}
	path = FixPath(path)
	switch len(path) {
	case 0: // Empty path
		var err error
		jv, err = emitJSON(&c.jsonData)
		if err != nil {
			return nil, err
		}
	case 1: // only table name provided
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return nil, err
		}
		jv, err = emitJSON(&vtable)
		if err != nil {
			return nil, err
		}
	case 2: // Second element must be table key
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return nil, err
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			return nil, err
		}
		jv, err = emitJSON(&ventry)
		if err != nil {
			return nil, err
		}
	case 3: // Third element must be field name
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return nil, err
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			return nil, err
		}
		vstr, vlist, err := DecodeJsonField(ventry, path[2])
		if err != nil {
			return nil, err
		}
		if vstr != nil {
			jv = []byte(`"` + *vstr + `"`)
		} else if vlist != nil {
			jv, err = json.Marshal(vlist)
			if err != nil {
				return nil, err
			}
		}
	case 4: // Fourth element must be list index
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return nil, err
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			return nil, err
		}
		_, vlist, err := DecodeJsonField(ventry, path[2])
		if err != nil {
			return nil, err
		}
		vstr, err := DecodeJsonListItem(vlist, path[3])
		if err != nil {
			return nil, err
		}
		if vstr != nil {
			jv = []byte(`"` + *vstr + `"`)
		} else {
			return nil, fmt.Errorf("Invalid db table Path %v", path)
		}
	default:
		log.V(2).Infof("Invalid db table Path %v", path)
		return nil, fmt.Errorf("Invalid db table Path %v", path)
	}
	return jv, nil
}

func (c *JsonClient) Add(path []string, value string) error {
	// The expect real db path could be in one of the formats:
	// <1> DB Table
	// <2> DB Table Key
	// <3> DB Table Key Field
	// <4> DB Table Key Field Index
	path = FixPath(path)
	switch len(path) {
	case 1: // only table name provided
		vtable, err := parseJson([]byte(value))
		if err != nil {
			return fmt.Errorf("Fail to parse %v", value)
		}
		v, ok := vtable.(map[string]interface{})
		if !ok {
			log.V(2).Infof("Invalid table %v", vtable)
			return fmt.Errorf("Invalid table %v", vtable)
		}
		c.jsonData[path[0]] = v
	case 2: // Second element must be table key
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			vtable = make(map[string]interface{})
			c.jsonData[path[0]] = vtable
		}
		ventry, err := parseJson([]byte(value))
		if err != nil {
			return fmt.Errorf("Fail to parse %v", value)
		}
		v, ok := ventry.(map[string]interface{})
		if !ok {
			log.V(2).Infof("Invalid entry %v", ventry)
			return fmt.Errorf("Invalid entry %v", ventry)
		}
		vtable[path[1]] = v
	case 3: // Third element must be field name
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			vtable = make(map[string]interface{})
			c.jsonData[path[0]] = vtable
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			ventry = make(map[string]interface{})
			vtable[path[1]] = ventry
		}
		vfield, err := parseJson([]byte(value))
		if err != nil {
			return fmt.Errorf("Fail to parse %v", value)
		}
		vstr, ok := vfield.(string)
		if ok {
			ventry[path[2]] = vstr
			return nil
		}
		vlist, ok := vfield.([]interface{})
		if ok {
			ventry[path[2]] = vlist
			return nil
		}
		log.V(2).Infof("Invalid field %v", vfield)
		return fmt.Errorf("Invalid field %v", vfield)
	case 4: // Fourth element must be list index
		id, err := strconv.Atoi(path[3])
		if err != nil {
			log.V(2).Infof("Invalid index %v", path[3])
			return fmt.Errorf("Invalid index %v", path[3])
		}
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			vtable = make(map[string]interface{})
			c.jsonData[path[0]] = vtable
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			ventry = make(map[string]interface{})
			vtable[path[1]] = ventry
		}
		vstr, vlist, err := DecodeJsonField(ventry, path[2])
		if err != nil {
			vlist = make([]interface{}, 0)
			ventry[path[2]] = vlist
		}
		if vstr != nil {
			log.V(2).Infof("Invalid target field %v", ventry)
			return fmt.Errorf("Invalid target field %v", ventry)
		}
		if id < 0 || id > len(vlist) {
			log.V(2).Infof("Invalid index %v", id)
			return fmt.Errorf("Invalid index %v", id)
		}
		if id == len(vlist) {
			vlist = append(vlist, "")
		} else {
			vlist = append(vlist[:id+1], vlist[id:]...)
		}
		ventry[path[2]] = vlist
		v, err := parseJson([]byte(value))
		if err != nil {
			return fmt.Errorf("Fail to parse %v", value)
		}
		vlist[id] = v
	default:
		log.V(2).Infof("Invalid db table Path %v", path)
		return fmt.Errorf("Invalid db table Path %v", path)
	}
		
	return nil
}

func (c *JsonClient) Replace(path []string, value string) error {
	err := c.Remove(path)
	if err != nil {
		return err
	}
	return c.Add(path, value)
}

func (c *JsonClient) Remove(path []string) error {
	// The expect real db path could be in one of the formats:
	// <1> DB Table
	// <2> DB Table Key
	// <3> DB Table Key Field
	// <4> DB Table Key Field Index
	path = FixPath(path)
	switch len(path) {
	case 1: // only table name provided
		_, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return err
		}
		delete(c.jsonData, path[0])
	case 2: // Second element must be table key
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return err
		}
		_, err = DecodeJsonEntry(vtable, path[1])
		if err != nil {
			return err
		}
		delete(vtable, path[1])
		if len(vtable) == 0 {
			delete(c.jsonData, path[0])
		}
	case 3: // Third element must be field name
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return err
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			return err
		}
		_, _, err = DecodeJsonField(ventry, path[2])
		if err != nil {
			return err
		}
		delete(ventry, path[2])
		if len(ventry) == 0 {
			delete(vtable, path[1])
		}
		if len(vtable) == 0 {
			delete(c.jsonData, path[0])
		}
	case 4: // Fourth element must be list index
		id, err := strconv.Atoi(path[3])
		if err != nil {
			log.V(2).Infof("Invalid index %v", path[3])
			return fmt.Errorf("Invalid index %v", path[3])
		}
		vtable, err := DecodeJsonTable(c.jsonData, path[0])
		if err != nil {
			return err
		}
		ventry, err := DecodeJsonEntry(vtable, path[1])
		if err != nil {
			return err
		}
		_, vlist, err := DecodeJsonField(ventry, path[2])
		if err != nil {
			return err
		}
		_, err = DecodeJsonListItem(vlist, path[3])
		if err != nil {
			return err
		}
		vlist = append(vlist[:id], vlist[id+1:]...)
		ventry[path[2]] = vlist
		if len(vlist) == 0 {
			delete(ventry, path[2])
		}
		if len(ventry) == 0 {
			delete(vtable, path[1])
		}
		if len(vtable) == 0 {
			delete(c.jsonData, path[0])
		}
	default:
		log.V(2).Infof("Invalid db table Path %v", path)
		return fmt.Errorf("Invalid db table Path %v", path)
	}

	return nil
}