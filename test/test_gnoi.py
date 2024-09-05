import pytest
from utils import gnoi_time, gnoi_setpackage, gnoi_switchcontrolprocessor
from utils import gnoi_reboot, gnoi_rebootstatus, gnoi_cancelreboot, gnoi_kill_process, gnoi_restart_process
from utils import gnoi_ping, gnoi_traceroute, gnmi_dump

class TestGNOI:

    def test_gnoi_time(self):
        ret, msg = gnoi_time()
        assert ret == 0, msg
        assert 'time' in msg, 'Invalid response: %s'%msg

    def test_gnoi_reboot(self):
        ret, old_cnt = gnmi_dump('DBUS config reload')
        assert ret == 0, 'Fail to read counter'

        ret, msg = gnoi_reboot(1, 0, 'Test reboot')
        assert ret == 0, msg

        ret, new_cnt = gnmi_dump('DBUS config reload')
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'

    def test_gnoi_rebootstatus(self):
        ret, msg = gnoi_rebootstatus()
        assert ret != 0, 'RebootStatus should fail' + msg
        assert 'Unimplemented' in msg

    def test_gnoi_cancelreboot(self):
        ret, msg = gnoi_cancelreboot('Test reboot')
        assert ret != 0, 'CancelReboot should fail' + msg
        assert 'Unimplemented' in msg

    def test_gnoi_killprocess(self):
        ret, old_cnt = gnmi_dump('DBUS stop service')
        assert ret == 0, 'Fail to read counter'

        json_data = '{"name": "snmp", "signal": 1}'
        ret, msg = gnoi_kill_process(json_data)
        assert ret == 0, msg

        ret, new_cnt = gnmi_dump('DBUS stop service')
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'

    def test_gnoi_restartprocess_unimplemented(self):
        ret, old_cnt = gnmi_dump('DBUS restart service')
        assert ret == 0, 'Fail to read counter'

        ret, msg = gnoi_restart_process('{"name": "snmp", "restart": true, "pid": 3}')
        assert ret != 0, msg
        
        ret, new_cnt = gnmi_dump('DBUS restart service')
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt, 'DBUS API invoked unexpectedly'

    def test_gnoi_restartprocess_valid(self):
        ret, old_cnt = gnmi_dump('DBUS restart service')
        assert ret == 0, 'Fail to read counter'

        ret, msg = gnoi_restart_process('{"name": "snmp", "restart": true, "signal": 1}')
        assert ret == 0, msg

        ret, new_cnt = gnmi_dump('DBUS restart service')
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'

    def test_gnoi_restartprocess_invalid(self):
        ret, old_cnt = gnmi_dump('DBUS restart service')
        assert ret == 0, 'Fail to read counter'

        ret, msg = gnoi_restart_process('{"name": "snmp", "restart": invalid}')
        assert ret != 0, msg

        ret, new_cnt = gnmi_dump('DBUS restart service')
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt, 'DBUS API invoked unexpectedly'