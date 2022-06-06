
import json
from utils import gnmi_set, gnmi_get

import pytest


test_data_update_normal = [
    [
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS',
            'value': {
                "qos_01": {"bw": "54321", "cps": "1000", "flows": "300"},
                "qos_02": {"bw": "6000", "cps": "200", "flows": "101"}
            }
        }
    ],
    [
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS/qos_01',
            'value': {"bw": "10001", "cps": "1001", "flows": "101"}
        },
        {
            'path': '/sonic-db:APPL_DB/DASH_QOS/qos_02',
            'value': {"bw": "10002", "cps": "1002", "flows": "102"}
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
        }
    ]
]

class TestGNMIApplDb:

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_update_normal(self, test_data):
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
        ret, msg_list1, msg_list2 = gnmi_get(get_list)
        assert ret == 0, "Invalid return code"
        assert len(msg_list1) or len(msg_list2), "Invalid msg: " + str(msg_list1) + str(msg_list2)
        for i, data in enumerate(test_data):
            hit = False
            for msg in msg_list1:
                rx_data = json.loads(msg)
                if data['value'] == rx_data:
                    hit = True
                    break
            for msg in msg_list2:
                if data['value'] == msg:
                    hit = True
                    break
            assert hit == True, 'No match for %s'%str(data['value'])

    @pytest.mark.parametrize("test_data", test_data_update_normal)
    def test_gnmi_delete_normal(self, test_data):
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
            ret, msg_list1, msg_list2 = gnmi_get([get])
            if ret != 0:
                continue
            for msg in msg_list1:
                assert msg == '{}', "Delete failed"
            assert len(msg_list2) == 0, "Delete failed"

