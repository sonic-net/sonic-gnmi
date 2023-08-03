
import json
from utils import gnmi_set, gnmi_get, gnmi_get_with_encoding, gnmi_get_proto

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
    def test_gnmi_update_normal_02(self, test_data):
        clear_appl_db('DASH_QOS')
        clear_appl_db('DASH_VNET')
        update_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            value = "x"
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)

        ret, msg = gnmi_set([], update_list, [])
        assert ret != 0, "Invalid json ietf value"

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
        ret, msg_list = gnmi_get_with_encoding(get_list, "ASCII")
        assert ret != 0, 'Encoding is not supported'
        hit = False
        exp = 'unsupported encoding'
        for msg in msg_list:
            if exp in msg:
                hit = True
                break
        assert hit == True, 'No expected error: %s'%exp

    def test_gnmi_update_proto_01(self):
        proto_bytes = b"\n\x010\x12$b6d54023-5d24-47de-ae94-8afe693dd1fc\x1a\x17\n\x12\x12\x10\r\xc0-\xdd\x82\xa3\x88;\x0fP\x84<\xaakc\x16\x10\x80\x01\x1a\x17\n\x12\x12\x10-\x0e\xf2\x7f\n~c_\xd8\xb7\x10\x84\x81\xd6'|\x10\x80\x01\x1a\x17\n\x12\x12\x10\x1bV\x89\xc8JW\x06\xfb\xad\b*fN\x9e(\x17\x10\x80\x01\x1a\x17\n\x12\x12\x107\xf9\xbc\xc0\x8d!s\xccVT\x88\x00\xf8\x9c\xce\x90\x10\x80\x01\x1a\x17\n\x12\x12\x10\tEb\x11Mf]\x12\x17x\x99\x80\xea\xd1u\xb4\x10\x80\x01\x1a\x17\n\x12\x12\x10\x1f\xd3\x1c\x89\x99\x16\xe7\x18\x91^0\x81\xb1\x04\x8c\x1e\x10\x80\x01\x1a\x17\n\x12\x12\x10\x06\x9e55\xdb\xb5&\x93\x99\xfaC\x81\x16P\xdc\x1d\x10\x80\x01\x1a\x17\n\x12\x12\x10&]U\x96e4\xf4\xd2'&\x04i\xdf\x8dA\x9f\x10\x80\x01\x1a\x17\n\x12\x12\x108\xd5\xa3*\xe7\x80\xdc\x1e\x80f\x94\xb7\xb6\x86~\xcd\x10\x80\x01\x1a\x17\n\x12\x12\x101\xf0@F\nu+}\x1e\"\\\\\xdb\x01\xe3\x82\x10\x80\x01\"\x05vnet1\"\x05vnet2\"\x05vnet1\"\x05vnet2\"\x05vnet2\"\x05vnet1\"\x05vnet2\"\x05vnet2\"\x05vnet1\"\x05vnet1"
        test_data = [
            {
                'update_path': '/sonic-db:APPL_DB/DASH_ROUTE_TABLE[key=F4939FEFC47E:20.2.2.0/24]',
                'get_path': '/sonic-db:APPL_DB/_DASH_ROUTE_TABLE[key=F4939FEFC47E:20.2.2.0/24]',
                'value': proto_bytes
            },
            {
                'update_path': '/sonic-db:APPL_DB/DASH_ROUTE_TABLE[key=F4939FEFC47E:30.3.3.0/24]',
                'get_path': '/sonic-db:APPL_DB/_DASH_ROUTE_TABLE[key=F4939FEFC47E:30.3.3.0/24]',
                'value': proto_bytes
            },
            {
                'update_path': '/sonic-db:APPL_DB/DASH_VNET_MAPPING_TABLE[key=Vnet2:20.2.2.2]',
                'get_path': '/sonic-db:APPL_DB/_DASH_VNET_MAPPING_TABLE[key=Vnet2:20.2.2.2]',
                'value': proto_bytes
            }
        ]
        update_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            value = data['value']
            file_name = 'update{}.txt'.format(i)
            file_object = open(file_name, 'wb')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':$./' + file_name)

        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        for i, data in enumerate(test_data):
            path = data['get_path']
            file_name = 'get{}.txt'.format(i)
            ret, msg = gnmi_get_proto([path], [file_name])
            assert ret == 0, msg
            result_bytes = open(file_name, 'rb').read()
            assert proto_bytes == result_bytes, 'get proto not equal to update proto'

    def test_gnmi_update_proto_02(self):
        update_path = '/sonic-db:APPL_DB/DASH_QOS'
        get_path = '/sonic-db:APPL_DB/_DASH_QOS[key=qos1]'
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
        update_list = [update_path + ':@./' + file_name]

        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg

        get_list = [get_path]
        file_list = ['get_proto.txt']
        ret, msg_list = gnmi_get_proto(get_list, file_list)
        assert ret != 0, 'Can not get result with proto encoding'

    def test_gnmi_update_proto_03(self):
        proto_bytes = b""
        test_data = [
            {
                'update_path': '/sonic-db:APPL_DB/DASH_ROUTE_TABLE[key=F4939FEFC47E:20.2.2.0/24]',
                'get_path': '/sonic-db:APPL_DB/_DASH_ROUTE_TABLE[key=F4939FEFC47E:20.2.2.0/24]',
                'value': proto_bytes
            },
            {
                'update_path': '/sonic-db:APPL_DB/DASH_ROUTE_TABLE[key=F4939FEFC47E:30.3.3.0/24]',
                'get_path': '/sonic-db:APPL_DB/_DASH_ROUTE_TABLE[key=F4939FEFC47E:30.3.3.0/24]',
                'value': proto_bytes
            },
            {
                'update_path': '/sonic-db:APPL_DB/DASH_VNET_MAPPING_TABLE[key=Vnet2:20.2.2.2]',
                'get_path': '/sonic-db:APPL_DB/_DASH_VNET_MAPPING_TABLE[key=Vnet2:20.2.2.2]',
                'value': proto_bytes
            }
        ]
        update_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            value = data['value']
            file_name = 'update{}.txt'.format(i)
            file_object = open(file_name, 'wb')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':$./' + file_name)

        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        for i, data in enumerate(test_data):
            path = data['get_path']
            file_name = 'get{}.txt'.format(i)
            ret, msg = gnmi_get_proto([path], [file_name])
            assert ret == 0, msg
            result_bytes = open(file_name, 'rb').read()
            assert proto_bytes == result_bytes, 'get proto not equal to update proto'

    def test_gnmi_delete_proto_01(self):
        test_data = [
            {
                'update_path': '/sonic-db:APPL_DB/DASH_ROUTE_TABLE/F4939FEFC47E:20.2.2.0\\\\/24',
            },
            {
                'update_path': '/sonic-db:APPL_DB/DASH_ROUTE_TABLE/F4939FEFC47E:30.3.3.0\\\\/24',
            },
            {
                'update_path': '/sonic-db:APPL_DB/DASH_VNET_MAPPING_TABLE/Vnet2:20.2.2.2',
            }
        ]
        delete_list = []
        for i, data in enumerate(test_data):
            path = data['update_path']
            delete_list.append(path)

        ret, msg = gnmi_set(delete_list, [], [])
        assert ret == 0, msg

