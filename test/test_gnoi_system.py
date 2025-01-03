"""
This module contains unit tests for GNOI system API.
See https://github.com/openconfig/gnoi/blob/main/system/system.proto.
"""
import pytest
from utils import gnmi_dump, gnoi_setpackage

class TestGNOISystem:
    """
    Test GNOI system API.
    """
    def test_system_setpackage_from_remote_url_success(self):
        """
        Test GNOI system set package success.
        """
        SONIC_VS_IMAGE_URL = "https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.bin"

        ret, download_start = gnmi_dump("DBUS image download")
        ret, install_start = gnmi_dump("DBUS image install")
        assert ret == 0, "Fail to read counter"

        ret, msg = gnoi_setpackage()
        assert ret == 0, msg

        ret, download_end = gnmi_dump("DBUS image download")
        ret, install_end = gnmi_dump("DBUS image install")
        assert ret == 0, "Fail to read counter"
        assert download_end == download_start + 1, "DBUS image download is not invoked"
        assert install_end == install_start + 1, "DBUS image install is not invoked"