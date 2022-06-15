import pytest
from utils import gnoi_time, gnoi_reboot, gnmi_dump

@pytest.mark.noauth
class TestGNOI:

    def test_gnoi_time(self):
        ret, msg = gnoi_time()
        assert ret == 0, msg
        assert 'time' in msg, 'Invalid response: %s'%msg

    def test_gnoi_reboot(self):
        ret, old_cnt = gnmi_dump("DBUS config reload")
        assert ret == 0, 'Fail to read counter'

        ret, msg = gnoi_reboot(1, 0, "Test reboot")
        assert ret == 0, msg

        ret, new_cnt = gnmi_dump("DBUS config reload")
        assert ret == 0, 'Fail to read counter'
        assert new_cnt == old_cnt+1, 'DBUS API is not invoked'
