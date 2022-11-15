import pytest
from utils import gnoi_time, gnoi_setpackage, gnoi_switchcontrolprocessor
from utils import gnoi_reboot, gnoi_rebootstatus, gnoi_cancelreboot
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

