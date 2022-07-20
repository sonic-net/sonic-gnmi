import pytest
from utils import gnmi_capabilities

@pytest.mark.noauth
class TestGNMICapabilities:

    def test_gnmi_cap(self):
        ret, msg = gnmi_capabilities()
        assert ret == 0, msg
        assert "sonic-db" in msg, "No sonic-db in msg: " + msg
        assert "sonic-yang" in msg, "No sonic-yang in msg: " + msg

