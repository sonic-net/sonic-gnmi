
import os
import time
from utils import gnmi_get, gnmi_subscribe_poll

import pytest


class TestGNMICountersDb:

    def test_gnmi_get_full_01(self):
        get_list = ['/sonic-db:COUNTERS_DB/localhost/']

        ret, msg_list = gnmi_get(get_list)
        assert ret != 0, 'Does not support to read all table in COUNTERS_DB'

    def test_gnmi_get_table_01(self):
        get_list = ['/sonic-db:COUNTERS_DB/localhost/COUNTERS']

        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Fail to read COUNTERS table, ' + msg_list[0]

    def test_gnmi_get_table_02(self):
        get_list = ['/sonic-db:COUNTERS_DB/localhost/COUNTERS/oid:0x1000000000003']

        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Fail to read COUNTERS table, ' + msg_list[0]

    def test_gnmi_get_table_03(self):
        get_list = ['/sonic-db:COUNTERS_DB/localhost/COUNTERS_PORT_NAME_MAP/Ethernet10']

        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Fail to read COUNTERS table, ' + msg_list[0]

    def test_gnmi_poll_table_01(self):
        path = "/COUNTERS_DB/localhost/COUNTERS_PORT_NAME_MAP/Ethernet10"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_poll(path, interval, cnt, timeout=0)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("COUNTERS_PORT_NAME_MAP") == cnt, 'Invalid result: ' + msg

    def test_gnmi_poll_table_02(self):
        path = "/COUNTERS_DB/localhost/COUNTERS/oid:0x1000000000003"
        cnt = 3
        interval = 1
        ret, msg = gnmi_subscribe_poll(path, interval, cnt, timeout=0)
        assert ret == 0, 'Fail to subscribe: ' + msg
        assert msg.count("oid:0x1000000000003") == cnt, 'Invalid result: ' + msg
