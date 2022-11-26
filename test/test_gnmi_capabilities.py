import pytest
from utils import gnmi_capabilities

class TestGNMICapabilities:

    def test_gnmi_cap(self):
        ret, msg = gnmi_capabilities()
        assert ret == 0, msg
        assert "sonic-db" in msg, "No sonic-db in msg: " + msg

