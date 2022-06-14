from utils import run_cmd, gnmi_get_with_password, gnmi_get_with_jwt

import pytest

@pytest.mark.auth
class TestGNMIAuth:

    def test_gnmi_get_with_pwd_neg(self):
        ret, msg_list = gnmi_get_with_password([], 'gnmitest', 'wrongpass')
        assert ret != 0, "Auth should fail"
        assert 'Unauthenticated' in msg_list[0]


    def test_gnmi_get_with_jwt_neg(self):
        jwt = 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.ElsKKULlzGtesThefMuj2_a6KIY9L5i2zDrBLHV-e0M'
        ret, msg_list = gnmi_get_with_jwt([], jwt)
        assert ret != 0, "Auth should fail"
        assert 'Unauthenticated' in msg_list[0]
