
import os
import json
import time
import threading, queue
from utils import gnmi_set, gnmi_get, gnmi_dump, run_cmd
from utils import gnmi_subscribe_poll, gnmi_subscribe_stream_sample, gnmi_subscribe_stream_onchange

import pytest


test_data_update_normal = [
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/PORT',
            'value': {
                'Ethernet4': {'admin_status': 'down'},
                'Ethernet8': {'admin_status': 'down'}
            }
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/PORT/Ethernet4/admin_status',
            'value': 'up'
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/PORT/Ethernet8/admin_status',
            'value': 'up'
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/PORT/Ethernet4',
            'value': {'admin_status': 'down'}
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/PORT/Ethernet8',
            'value': {'admin_status': 'down'}
        }
    ]
]

test_json_checkpoint = {
    "DASH_QOS": {
        'qos_01': {'bw': '54321', 'cps': '1000', 'flows': '300'},
        'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
    },
    "DASH_VNET": {
        'vnet_3721': {
            'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
        }
    }
}

test_data_checkpoint = [
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_QOS',
            'value': {
                'qos_01': {'bw': '54321', 'cps': '1000', 'flows': '300'},
                'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
            }
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_VNET',
            'value': {
                'vnet_3721': {
                    'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
                }
            }
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_QOS/qos_01',
            'value': {'bw': '54321', 'cps': '1000', 'flows': '300'},
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_QOS/qos_02',
            'value': {'bw': '6000', 'cps': '200', 'flows': '101'}
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721',
            'value': {
                'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
            }
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_QOS/qos_01/flows',
            'value': '300'
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_QOS/qos_02/bw',
            'value': '6000'
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721/address_spaces',
            'value': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
        }
    ],
    [
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721/address_spaces/0',
            'value': "10.250.0.0"
        },
        {
            'path': '/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721/address_spaces/1',
            'value': "192.168.3.0"
        }
    ]
]

patch_file = '/tmp/gcu.patch'
config_file = '/tmp/config_db.json.tmp'
checkpoint_file = '/etc/sonic/config.cp.json'

def create_dir(path):
    isExists = os.path.exists(path)
    if not isExists:
        os.makedirs(path)

def create_checkpoint(file_name, text):
    create_dir(os.path.dirname(file_name))
    file_object = open(file_name, 'w')
    file_object.write(text)
    file_object.close()
    return

class TestGNMIConfigDb:

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_incremental_update(self, test_data):
        create_checkpoint(checkpoint_file, '{}')

        update_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)

        ret, old_apply_patch_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        ret, old_create_checkpoint_cnt = gnmi_dump("DBUS create checkpoint")
        assert ret == 0, 'Fail to read counter'
        ret, old_delete_checkpoint_cnt = gnmi_dump("DBUS delete checkpoint")
        assert ret == 0, 'Fail to read counter'
        ret, old_config_save_cnt = gnmi_dump("DBUS config save")
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
                if test_path == '/sonic-db:CONFIG_DB/localhost' + patch_data['path'] and test_value == patch_data['value']:
                    break
            else:
                pytest.fail('No item in patch: %s'%str(item))
        ret, new_apply_patch_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        assert new_apply_patch_cnt == old_apply_patch_cnt + 1, 'DBUS API is not invoked'
        ret, new_create_checkpoint_cnt = gnmi_dump("DBUS create checkpoint")
        assert ret == 0, 'Fail to read counter'
        assert new_create_checkpoint_cnt == old_create_checkpoint_cnt + 1, 'DBUS API is not invoked'
        ret, new_delete_checkpoint_cnt = gnmi_dump("DBUS delete checkpoint")
        assert ret == 0, 'Fail to read counter'
        assert new_delete_checkpoint_cnt == old_delete_checkpoint_cnt + 1, 'DBUS API is not invoked'
        ret, new_config_save_cnt = gnmi_dump("DBUS config save")
        assert ret == 0, 'Fail to read counter'
        assert new_config_save_cnt == old_config_save_cnt + 1, 'DBUS API is not invoked'

    @pytest.mark.parametrize("test_data", test_data_checkpoint)
    def test_gnmi_incremental_delete(self, test_data):
        create_checkpoint(checkpoint_file, json.dumps(test_json_checkpoint))

        if os.path.exists(patch_file):
            os.remove(patch_file)
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
                if test_path == '/sonic-db:CONFIG_DB/localhost' + patch_data['path']:
                    break
            else:
                pytest.fail('No item in patch: %s'%str(item))
        ret, new_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API should not be invoked'

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_incremental_delete_negative(self, test_data):
        create_checkpoint(checkpoint_file, '{}')
        if os.path.exists(patch_file):
            os.remove(patch_file)

        delete_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            delete_list.append(path)

        ret, old_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        ret, msg = gnmi_set(delete_list, [], [])
        assert ret == 0, msg
        assert not os.path.exists(patch_file), "Should not generate patch file"
        ret, new_cnt = gnmi_dump("DBUS apply patch db")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt, 'DBUS API should not be invoked'

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_incremental_replace(self, test_data):
        test_config = {
            "PORT": {
                'Ethernet4': {'admin_status': 'down'},
                'Ethernet8': {'admin_status': 'down'}
            }
        }
        create_checkpoint(checkpoint_file, json.dumps(test_config))

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
                assert patch_data['op'] == 'replace', "Invalid operation"
                if test_path == '/sonic-db:CONFIG_DB/localhost' + patch_data['path'] and test_value == patch_data['value']:
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
        delete_list = ['/sonic-db:CONFIG_DB/localhost/']
        update_list = ['/sonic-db:CONFIG_DB/localhost/' + ':@./' + file_name]

        ret, msg = gnmi_set(delete_list, update_list, [])
        assert ret == 0, msg
        assert os.path.exists(config_file), "No config file"
        with open(config_file,'r') as cf:
            config_json = json.load(cf)
        assert test_data == config_json, "Wrong config file"

    def test_gnmi_full_negative(self):
        delete_list = ['/sonic-db:CONFIG_DB/localhost/']
        update_list = ['/sonic-db:CONFIG_DB/localhost/' + ':abc']

        ret, msg = gnmi_set(delete_list, update_list, [])
        assert ret != 0, 'Invalid ietf_json_val'
        assert 'IETF JSON' in msg

    @pytest.mark.parametrize("test_data", test_data_checkpoint)
    def test_gnmi_get_checkpoint(self, test_data):
        if os.path.isfile(checkpoint_file):
            os.remove(checkpoint_file)

        get_list = []
        for data in test_data:
            path = data['path']
            get_list.append(path)

        ret, msg_list = gnmi_get(get_list)
        if ret == 0:
            for msg in msg_list:
                assert msg == '{}', 'Invalid result'

        text = json.dumps(test_json_checkpoint)
        create_checkpoint(checkpoint_file, text)

        get_list = []
        for data in test_data:
            path = data['path']
            value = json.dumps(data['value'])
            get_list.append(path)

        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Invalid return code'
        assert len(msg_list), 'Invalid msg: ' + str(msg_list)
        for data in test_data:
            hit = False
            for msg in msg_list:
                rx_data = json.loads(msg)
                if data['value'] == rx_data:
                    hit = True
                    break
            assert hit == True, 'No match for %s'%str(data['value'])

    def test_gnmi_get_checkpoint_negative_01(self):
        text = json.dumps(test_json_checkpoint)
        create_checkpoint(checkpoint_file, text)

        get_list = ['/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721/address_spaces/0/abc']
 
        ret, _ = gnmi_get(get_list)
        assert ret != 0, 'Invalid path'

    def test_gnmi_get_checkpoint_negative_02(self):
        text = json.dumps(test_json_checkpoint)
        create_checkpoint(checkpoint_file, text)

        get_list = ['/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721/address_spaces/abc']
 
        ret, _ = gnmi_get(get_list)
        assert ret != 0, 'Invalid path'

    def test_gnmi_get_checkpoint_negative_03(self):
        text = json.dumps(test_json_checkpoint)
        create_checkpoint(checkpoint_file, text)

        get_list = ['/sonic-db:CONFIG_DB/localhost/DASH_VNET/vnet_3721/address_spaces/1000']
 
        ret, _ = gnmi_get(get_list)
        assert ret != 0, 'Invalid path'

    def test_gnmi_get_full_01(self):
        get_list = ['/sonic-db:CONFIG_DB/localhost/']

        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Fail to get full config'
        assert "NULL" not in msg_list[0], 'Invalid config'
        # Config must be valid json
        config = json.loads(msg_list[0])

    def test_gnmi_update_invalid_01(self):
        path = '/sonic-db:CONFIG_DB/'
        value = {
            'qos_01': {'bw': '54321', 'cps': '1000', 'flows': '300'},
            'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
        }
        update_list = []
        text = json.dumps(value)
        file_name = 'update.txt'
        file_object = open(file_name, 'w')
        file_object.write(text)
        file_object.close()
        update_list = [path + ':@./' + file_name]

        ret, msg = gnmi_set([], update_list, [])
        assert ret != 0, "Failed to detect invalid update path"
        assert "Invalid elem length" in msg, msg

    def test_gnmi_delete_invalid_01(self):
        path = '/sonic-db:CONFIG_DB/'
        delete_list = [path]

        ret, msg = gnmi_set(delete_list, [], [])
        assert ret != 0, "Failed to detect invalid delete path"
        assert "Invalid elem length" in msg, msg

    def test_gnmi_replace_invalid_01(self):
        path = '/sonic-db:CONFIG_DB/'
        value = {
            'qos_01': {'bw': '54321', 'cps': '1000', 'flows': '300'},
            'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
        }
        update_list = []
        text = json.dumps(value)
        file_name = 'update.txt'
        file_object = open(file_name, 'w')
        file_object.write(text)
        file_object.close()
        update_list = [path + ':@./' + file_name]

        ret, msg = gnmi_set([], [], update_list)
        assert ret != 0, "Failed to detect invalid replace path"
        assert "Invalid elem length" in msg, msg

    def test_gnmi_poll_01(self):
        path = "/CONFIG_DB/localhost/DEVICE_METADATA"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_poll(path, interval, cnt, timeout=0)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") == cnt, 'Invalid result: ' + msg

    def test_gnmi_poll_invalid_01(self):
        path = "/CONFIG_DB/localhost/INVALID_TABLE"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_poll(path, interval, cnt, timeout=10)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") == 0, 'Invalid result: ' + msg
        assert "rpc error" in msg, 'Invalid result: ' + msg

    def test_gnmi_stream_sample_01(self):
        # Subscribe table
        path = "/CONFIG_DB/localhost/DEVICE_METADATA"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_stream_sample(path, interval, cnt, timeout=10)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") >= cnt, 'Invalid result: ' + msg

    def test_gnmi_stream_sample_02(self):
        # Subscribe table key
        path = "/CONFIG_DB/localhost/DEVICE_METADATA/localhost"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_stream_sample(path, interval, cnt, timeout=10)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") >= cnt, 'Invalid result: ' + msg

    def test_gnmi_stream_sample_03(self):
        # Subscribe table field
        path = "/CONFIG_DB/localhost/DEVICE_METADATA/localhost/bgp_asn"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_stream_sample(path, interval, cnt, timeout=10)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") >= cnt, 'Invalid result: ' + msg

    def test_gnmi_stream_sample_invalid_01(self):
        path = "/CONFIG_DB/localhost/DEVICE_METADATA/localhost/invalid_field"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_stream_sample(path, interval, cnt, timeout=10)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") == 0, 'Invalid result: ' + msg
        assert "rpc error" in msg, 'Invalid result: ' + msg

    def test_gnmi_stream_onchange_01(self):
        # Init bgp_asn
        cmd = r'redis-cli -n 4 hset "DEVICE_METADATA|localhost" bgp_asn 65100'
        run_cmd(cmd)

        result_queue = queue.Queue()
        cnt = 3

        def worker():
            # Subscribe table
            path = "/CONFIG_DB/localhost/DEVICE_METADATA"
            ret, msg = gnmi_subscribe_stream_onchange(path, cnt+3, timeout=10)
            result_queue.put((ret, msg))

        t = threading.Thread(target=worker)
        t.start()

        # Modify bgp_asn
        time.sleep(0.5)
        cmd = r'redis-cli -n 4 hdel "DEVICE_METADATA|localhost" bgp_asn '
        run_cmd(cmd)
        for i in range(cnt):
            time.sleep(0.5)
            cmd = r'redis-cli -n 4 hset "DEVICE_METADATA|localhost" bgp_asn ' + str(i+1000)
            run_cmd(cmd)

        t.join()
        ret, msg = result_queue.get()
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") >= cnt+1, 'Invalid result: ' + msg

    def test_gnmi_stream_onchange_02(self):
        # Init bgp_asn
        cmd = r'redis-cli -n 4 hset "DEVICE_METADATA|localhost" bgp_asn 65100'
        run_cmd(cmd)

        result_queue = queue.Queue()
        cnt = 3

        def worker():
            # Subscribe table key
            path = "/CONFIG_DB/localhost/DEVICE_METADATA/localhost"
            ret, msg = gnmi_subscribe_stream_onchange(path, cnt+3, timeout=10)
            result_queue.put((ret, msg))

        t = threading.Thread(target=worker)
        t.start()

        # Modify bgp_asn
        time.sleep(0.5)
        cmd = r'redis-cli -n 4 hdel "DEVICE_METADATA|localhost" bgp_asn '
        run_cmd(cmd)
        for i in range(cnt):
            time.sleep(0.5)
            cmd = r'redis-cli -n 4 hset "DEVICE_METADATA|localhost" bgp_asn ' + str(i+1000)
            run_cmd(cmd)

        t.join()
        ret, msg = result_queue.get()
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("bgp_asn") >= cnt+1, 'Invalid result: ' + msg

    def test_gnmi_stream_onchange_03(self):
        # Init bgp_asn
        cmd = r'redis-cli -n 4 hset "DEVICE_METADATA|localhost" bgp_asn 65100'
        run_cmd(cmd)

        result_queue = queue.Queue()
        cnt = 3

        def worker():
            # Subscribe table field
            path = "/CONFIG_DB/localhost/DEVICE_METADATA/localhost/bgp_asn"
            ret, msg = gnmi_subscribe_stream_onchange(path, cnt+1, timeout=10)
            result_queue.put((ret, msg))

        t = threading.Thread(target=worker)
        t.start()

        # Modify bgp_asn
        for i in range(cnt):
            time.sleep(0.5)
            cmd = r'redis-cli -n 4 hset "DEVICE_METADATA|localhost" bgp_asn ' + str(i+1000)
            run_cmd(cmd)

        t.join()
        ret, msg = result_queue.get()
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert "bgp_asn" in msg, 'Invalid result: ' + msg
