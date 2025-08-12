package show_client

import (
	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const VlanTable= "VLAN"
const VlanInterfaceTable= "VLAN_INTERFACE"
const VlanMemberTable= "VLAN_MEMBER"

func getVlanBrief(options sdc.OptionMap) ([]byte, error) {
	queriesVlan := [][]string{
		{"CONFIG_DB", VlanTable},
	}

	queriesVlanInterface := [][]string{
		{"CONFIG_DB", VlanInterfaceTable},
	}

	queriesVlanMember := [][]string{
		{"CONFIG_DB", VlanMemberTable},
	}

	vlanData, derr := GetMapFromQueries(queriesVlan)
	vlanInterfaceData, ierr := GetMapFromQueries(queriesVlanInterface)
	vlanMemberData, merr := GetMapFromQueries(queriesVlanMember)

    vlanConfigs := MergeMaps(vlanData, vlanInterfaceData, vlanMemberData)

	if err != nil {
		log.Errorf("Unable to get data from queries %v, got err: %v", queries, err)
		return nil, err
	}
	return data, nil
}
