
import json
from utils import gnmi_set, gnmi_get, gnmi_get_with_encoding

import pytest


test_data_update_normal = [
    [
        {
            'update_path': '/sonic-db:APPL_DB/DASH_QOS',
            'get_path': '/sonic-db:APPL_DB/_DASH_QOS',
            'value': {
                'qos_01': {'bw': '54321', 'cps': '1000', 'flows': '300'},
                'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
            }
        },
        {
            'update_path': '/sonic-db:APPL_DB/DASH_VNET',
            'get_path': '/sonic-db:APPL_DB/_DASH_VNET',
            'value': {
                'Vnet3721': {
                    'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
                }
            }
        }
    ],
    [
        {
            'update_path': '/sonic-db:APPL_DB/DASH_QOS/qos_01',
            'get_path': '/sonic-db:APPL_DB/_DASH_QOS/qos_01',
            'value': {'bw': '10001', 'cps': '1001', 'flows': '101'}
        },
        {
            'update_path': '/sonic-db:APPL_DB/DASH_QOS/qos_02',
            'get_path': '/sonic-db:APPL_DB/_DASH_QOS/qos_02',
            'value': {'bw': '10002', 'cps': '1002', 'flows': '102'}
        },
        {
            'update_path': '/sonic-db:APPL_DB/DASH_VNET/Vnet3721',
            'get_path': '/sonic-db:APPL_DB/_DASH_VNET/Vnet3721',
            'value': {
                'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
            }
        }
    ]
]

def clear_appl_db(table_name):
    prefix = '/sonic-db:APPL_DB'
    get_path = prefix + '/_' + table_name
    ret, msg_list = gnmi_get([get_path])
    if ret != 0:
        return
    for msg in msg_list:
        rx_data = json.loads(msg)
        delete_list = []
        for key in rx_data.keys():
            delete_path = prefix + '/' + table_name + '/' + key
            delete_list.append(delete_path)
        if len(delete_list):
            ret, msg = gnmi_set(delete_list, [], [])
            assert ret == 0, msg

class TestGNMIApplDb:

    @pytest.mark.parametrize('test_data', test_data_update_normal)
    def test_gnmi_update_normal_01(self, test_data):
        clear_appl_db('DASH_QOS')
        clear_appl_db('DASH_VNET')
        update_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            get_path = data['get_path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)
            get_list.append(get_path)

        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Invalid return code'
        assert len(msg_list), 'Invalid msg: ' + str(msg_list)
        for i, data in enumerate(test_data):
            hit = False
            for msg in msg_list:
                rx_data = json.loads(msg)
                if data['value'] == rx_data:
                    hit = True
                    break
            assert hit == True, 'No match for %s'%str(data['value'])

    @pytest.mark.parametrize('test_data', test_data_update_normal)
    def test_gnmi_delete_normal_01(self, test_data):
        delete_list = []
        update_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            path_length = path.count('/')
            # path length is 2, path has table name, and has no key
            # there's no consumer for unit test, and gnmi cannot delete temporary state table
            if path_length <= 2:
                continue
            get_path = data['get_path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)
            delete_list.append(path)
            get_list.append(get_path)

        if len(update_list) == 0:
            return
        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        ret, msg = gnmi_set(delete_list, [], [])
        assert ret == 0, msg
        for get in get_list:
            ret, msg_list = gnmi_get([get])
            if ret != 0:
                continue
            for msg in msg_list:
                assert msg == '{}', 'Delete failed'

    @pytest.mark.parametrize('test_data', test_data_update_normal)
    def test_gnmi_replace_normal_01(self, test_data):
        clear_appl_db('DASH_QOS')
        clear_appl_db('DASH_VNET')
        replace_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            get_path = data['get_path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            replace_list.append(path + ':@./' + file_name)
            get_list.append(get_path)

        ret, msg = gnmi_set([], [], replace_list)
        assert ret == 0, msg
        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Invalid return code'
        assert len(msg_list), 'Invalid msg: ' + str(msg_list)
        for i, data in enumerate(test_data):
            hit = False
            for msg in msg_list:
                rx_data = json.loads(msg)
                if data['value'] == rx_data:
                    hit = True
                    break
            assert hit == True, 'No match for %s'%str(data['value'])

    @pytest.mark.parametrize('test_data', test_data_update_normal)
    def test_gnmi_replace_normal_02(self, test_data):
        replace_list = []
        update_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            path_length = path.count('/')
            # path length is 2, path has table name, and has no key
            # there's no consumer for unit test, and gnmi cannot delete temporary state table
            if path_length <= 2:
                continue
            get_path = data['get_path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)
            replace_list.append(path + ':#')
            get_list.append(get_path)

        if len(update_list) == 0:
            return
        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        ret, msg = gnmi_set([], [], replace_list)
        assert ret == 0, msg
        for get in get_list:
            ret, msg_list = gnmi_get([get])
            if ret != 0:
                continue
            for msg in msg_list:
                assert msg == '{}', 'Delete failed'

    def test_gnmi_invalid_path_01(self):
        path = '/sonic-db:APPL_DB/DASH_QOS/qos_01/bw'
        value = '300'
        update_list = []
        text = json.dumps(value)
        file_name = 'update.txt'
        file_object = open(file_name, 'w')
        file_object.write(text)
        file_object.close()
        update_list = [path + ':@./' + file_name]

        ret, msg = gnmi_set([], update_list, [])
        assert ret != 0, 'Invalid path'
        assert 'Unsupported path' in msg

    def test_gnmi_invalid_origin_01(self):
        path1 = '/sonic-db:APPL_DB/DASH_QOS'
        path2 = '/sonic-yang:APPL_DB/DASH_QOS'
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
        update_list = [path1 + ':@./' + file_name, path2 + ':@./' + file_name]

        ret, msg = gnmi_set([], update_list, [])
        assert ret != 0, 'Origin is invalid'
        assert 'Origin conflict' in msg

        get_list = [path1, path2]
        ret, msg_list = gnmi_get(get_list)
        assert ret != 0, 'Origin is invalid'
        hit = False
        exp = 'Origin conflict'
        for msg in msg_list:
            if exp in msg:
                hit = True
                break
        assert hit == True, 'No expected error: %s'%exp

    def test_gnmi_invalid_target_01(self):
        path = '/sonic-db:INVALID_DB/DASH_QOS'
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
        assert ret != 0, 'Target is invalid'
        assert 'Invalid target' in msg

        get_list = [path]
        ret, msg_list = gnmi_get(get_list)
        assert ret != 0, 'Target is invalid'
        hit = False
        exp = 'Invalid target'
        for msg in msg_list:
            if exp in msg:
                hit = True
                break
        assert hit == True, 'No expected error: %s'%exp

    def test_gnmi_invalid_target_02(self):
        path = '/sonic-db:ASIC_DB/DASH_QOS'
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
        assert ret != 0, 'Target is invalid'
        assert 'Set RPC does not support ASIC_DB' in msg

    def test_gnmi_invalid_target_03(self):
        path1 = '/sonic-db:APPL_DB/DASH_QOS'
        path2 = '/sonic-db:CONFIG_DB/DASH_QOS'
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
        update_list = [path1 + ':@./' + file_name, path2 + ':@./' + file_name]

        ret, msg = gnmi_set([], update_list, [])
        assert ret != 0, 'Target is invalid'
        assert 'Target conflict' in msg

        get_list = [path1, path2]
        ret, msg_list = gnmi_get(get_list)
        assert ret != 0, 'Target is invalid'
        hit = False
        exp = 'Target conflict'
        for msg in msg_list:
            if exp in msg:
                hit = True
                break
        assert hit == True, 'No expected error: %s'%exp

    def test_gnmi_invalid_encoding(self):
        path = '/sonic-db:APPL_DB/DASH_QOS'
        get_list = [path]
        ret, msg_list = gnmi_get_with_encoding(get_list, "PROTO")
        assert ret != 0, 'Encoding is not supported'
        hit = False
        exp = 'unsupported encoding'
        for msg in msg_list:
            if exp in msg:
                hit = True
                break
        assert hit == True, 'No expected error: %s'%exp

