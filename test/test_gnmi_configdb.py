
import os
import json
import time
from utils import gnmi_set, gnmi_get, gnmi_dump

import pytest


test_data_update_normal = [
    [
        {
            'path': '/sonic-db:CONFIG_DB/PORT',
            'value': {
                'Ethernet4': {'admin_status': 'down'},
                'Ethernet8': {'admin_status': 'down'}
            }
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/PORT/Ethernet4/admin_status',
            'value': 'up'
        },
        {
            'path': '/sonic-db:CONFIG_DB/PORT/Ethernet8/admin_status',
            'value': 'up'
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/PORT/Ethernet4',
            'value': {'admin_status': 'down'}
        },
        {
            'path': '/sonic-db:CONFIG_DB/PORT/Ethernet8',
            'value': {'admin_status': 'down'}
        }
    ]
]

patch_file = '/etc/sonic/gnmi/gcu.patch'
config_file = '/etc/sonic/gnmi/config_db.json.tmp'

class TestGNMIApplDb:

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_incremental_update(self, test_data):
        update_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)

        ret, old_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        assert os.path.exists(patch_file), "No patch file"
        with open(patch_file,'r') as pf:
            patch_json = json.load(pf)
        for item in test_data:
            test_path = item['path']
            test_value = item['value']
            for patch_data in patch_json:
                assert patch_data['op'] == 'add', "Invalid operation"
                if test_path == '/sonic-db:CONFIG_DB' + patch_data['path'] and test_value == patch_data['value']:
                    break
            else:
                pytest.fail('No item in patch: %s'%str(item))
        ret, new_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_incremental_delete(self, test_data):
        delete_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            delete_list.append(path)

        ret, old_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        ret, msg = gnmi_set(delete_list, [], [])
        assert ret == 0, msg
        assert os.path.exists(patch_file), "No patch file"
        with open(patch_file,'r') as pf:
            patch_json = json.load(pf)
        for item in test_data:
            test_path = item['path']
            for patch_data in patch_json:
                assert patch_data['op'] == 'remove', "Invalid operation"
                assert 'value' not in patch_data, 'Invalid patch %s'%(str(patch_data))
                if test_path == '/sonic-db:CONFIG_DB' + patch_data['path']:
                    break
            else:
                pytest.fail('No item in patch: %s'%str(item))
        ret, new_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_incremental_replace(self, test_data):
        replace_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            replace_list.append(path + ':@./' + file_name)

        ret, old_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        ret, msg = gnmi_set([], [], replace_list)
        assert ret == 0, msg
        assert os.path.exists(patch_file), "No patch file"
        with open(patch_file,'r') as pf:
            patch_json = json.load(pf)
        for item in test_data:
            test_path = item['path']
            test_value = item['value']
            for patch_data in patch_json:
                assert patch_data['op'] == 'add', "Invalid operation"
                if test_path == '/sonic-db:CONFIG_DB' + patch_data['path'] and test_value == patch_data['value']:
                    break
            else:
                pytest.fail('No item in patch: %s'%str(item))
        ret, new_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'

    def test_gnmi_full(self):
        test_data = {
            'field_01': '20001',
            'field_02': '20002',
            'field_03': '20003',
            'field_04': {'item_01': 'aaaa', 'item_02': 'xxxxx'}
        }
        file_name = 'config_db.test'
        file_object = open(file_name, 'w')
        value = json.dumps(test_data)
        file_object.write(value)
        file_object.close()
        delete_list = ['/sonic-db:CONFIG_DB/']
        update_list = ['/sonic-db:CONFIG_DB/' + ':@./' + file_name]

        ret, old_cnt = gnmi_dump("DBUS config reload")
        assert ret == 0, 'Fail to read counter'
        ret, msg = gnmi_set(delete_list, update_list, [])
        assert ret == 0, msg
        assert os.path.exists(config_file), "No config file"
        with open(config_file,'r') as cf:
            config_json = json.load(cf)
        assert test_data == config_json, "Wrong config file"
        time.sleep(12)
        ret, new_cnt = gnmi_dump("DBUS config reload")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'
