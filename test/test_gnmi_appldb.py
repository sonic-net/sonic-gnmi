
import json
from utils import gnmi_set, gnmi_get

import pytest


test_data_update_normal = [
    [
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS',
            'value': {
                'qos_01': {'bw': '54321', 'cps': '1000', 'flows': '300'},
                'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
            }
        },
        {
            'path': '/sonic-db:APPL_DB/DASH_VNET',
            'value': {
                'vnet_3721': {
                    'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
                }
            }
        }
    ],
    [
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS/qos_01',
            'value': {'bw': '10001', 'cps': '1001', 'flows': '101'}
        },
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS/qos_02',
            'value': {'bw': '10002', 'cps': '1002', 'flows': '102'}
        },
        {
            'path': '/sonic-db:APPL_DB/DASH_VNET/vnet_3721',
            'value': {
                'address_spaces': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
            }
        }
    ],
    [
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS/qos_01/bw',
            'value': '20001'
        },
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS/qos_02/flows',
            'value': '202'
        },
        {
            'path': '/sonic-db:APPL_DB/DASH_VNET/vnet_3721/address_spaces',
            'value': ["10.250.0.0", "192.168.3.0", "139.66.72.9"]
        }
    ]
]

@pytest.mark.noauth
class TestGNMIApplDb:

    @pytest.mark.parametrize('test_data', test_data_update_normal)
    def test_gnmi_update_normal_01(self, test_data):
        update_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)
            get_list.append(path)

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
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)
            delete_list.append(path)
            get_list.append(path)

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
        replace_list = []
        get_list = []
        for i, data in enumerate(test_data):
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            replace_list.append(path + ':@./' + file_name)
            get_list.append(path)

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
            path = data['path']
            value = json.dumps(data['value'])
            file_name = 'update' + str(i)
            file_object = open(file_name, 'w')
            file_object.write(value)
            file_object.close()
            update_list.append(path + ':@./' + file_name)
            replace_list.append(path + ':#')
            get_list.append(path)

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

    def test_gnmi_list_normal_01(self):
        test_data_1 = {
            'path': '/sonic-db:APPL_DB/DASH_VNET/vnet_3721/address_spaces',
            'value': ["10.250.0.0", "6.6.6.6"]
        }
        test_data_2 = {
            'path': '/sonic-db:APPL_DB/DASH_VNET/vnet_3721/address_spaces/0',
            'value': "192.168.3.10"
        }
        test_data_3 = {
            'path': '/sonic-db:APPL_DB/DASH_VNET/vnet_3721/address_spaces/100',
            'value': "8.8.8.8"
        }

        # Update test_data_1
        path = test_data_1['path']
        value = json.dumps(test_data_1['value'])
        file_name = 'update'
        file_object = open(file_name, 'w')
        file_object.write(value)
        file_object.close()
        update_list = [path + ':@./' + file_name]
        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        get_list = [path]
        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Invalid return code'
        assert len(msg_list), 'Invalid msg: ' + str(msg_list)
        hit = False
        for msg in msg_list:
            rx_data = json.loads(msg)
            if test_data_1['value'] == rx_data:
                hit = True
                break
        assert hit == True, 'No match for %s'%str(test_data_1['value'])

        # Update test_data_2
        path = test_data_2['path']
        value = json.dumps(test_data_2['value'])
        file_name = 'update'
        file_object = open(file_name, 'w')
        file_object.write(value)
        file_object.close()
        update_list = [path + ':@./' + file_name]
        ret, msg = gnmi_set([], update_list, [])
        assert ret == 0, msg
        get_list = [path]
        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Invalid return code'
        assert len(msg_list), 'Invalid msg: ' + str(msg_list)
        hit = False
        for msg in msg_list:
            rx_data = json.loads(msg)
            if test_data_2['value'] == rx_data:
                hit = True
                break
        assert hit == True, 'No match for %s'%str(test_data_2['value'])

        # Update test_data_3
        path = test_data_3['path']
        value = json.dumps(test_data_3['value'])
        file_name = 'update'
        file_object = open(file_name, 'w')
        file_object.write(value)
        file_object.close()
        update_list = [path + ':@./' + file_name]
        ret, msg = gnmi_set([], update_list, [])
        assert ret != 0, msg

        # Delete test_data_3
        path = test_data_3['path']
        delete_list = [path]
        ret, msg = gnmi_set(delete_list, [], [])
        assert ret != 0, msg

        # Delete test_data_2
        path = test_data_2['path']
        delete_list = [path]
        ret, msg = gnmi_set(delete_list, [], [])
        assert ret == 0, msg

        # Delete test_data_1
        path = test_data_1['path']
        delete_list = [path]
        ret, msg = gnmi_set(delete_list, [], [])
        assert ret == 0, msg
        get_list = [path]
        ret, msg_list = gnmi_get(get_list)
        assert ret != 0, 'Invalid return code'

    def test_gnmi_set_empty_01(self):
        ret, msg = gnmi_set([], [], [])
        assert ret != 0, msg

    def test_gnmi_invalid_origin_01(self):
        path = '/sonic-invalid:APPL_DB/DASH_QOS'
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
        assert ret != 0, 'Origin is invalid'
        assert 'Invalid origin' in msg

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
