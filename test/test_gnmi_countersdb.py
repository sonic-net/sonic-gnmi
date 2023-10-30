
import os
import time
from utils import gnmi_set, gnmi_get, gnmi_dump

import pytest


class TestGNMICountersDb:

    def test_gnmi_get_full_01(self):
        get_list = ['/sonic-db:COUNTERS_DB/']

        ret, msg_list = gnmi_get(get_list)
        assert ret != 0, 'Does not support to read all table in COUNTERS_DB'

    def test_gnmi_get_table_01(self):
        get_list = ['/sonic-db:COUNTERS_DB/COUNTERS']

        ret, msg_list = gnmi_get(get_list)
        assert ret == 0, 'Fail to read COUNTERS table, ' + msg_list[0]
