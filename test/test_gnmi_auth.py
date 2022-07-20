from utils import run_cmd, gnmi_set_with_password, gnmi_set_with_jwt
from utils import gnoi_authenticate, gnoi_refresh_with_jwt

import re
import pwd
import json
import pytest

def del_user(username):
    run_cmd('sudo userdel %s'%(username))
    try:
        return pwd.getpwnam(username) == None
    except KeyError as err:
        return True

def add_user(username):

    run_cmd('sudo useradd %s'%(username))
    try:
        return pwd.getpwnam(username) != None
    except KeyError as err:
        return False

@pytest.mark.auth
class TestGNMIAuth:

    def test_gnmi_set_with_pwd_neg(self):
        username = 'gnmitest1'
        password = 'password1'
        ret = del_user(username)
        if ret == False:
            print("Fail to add user, skip this test...")
            return
        path = '/sonic-db:APPL_DB/DASH_QOS'
        value = {
            'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
        }
        update_list = []
        text = json.dumps(value)
        file_name = 'update.txt'
        file_object = open(file_name, 'w')
        file_object.write(text)
        file_object.close()
        update_list = [path + ':@./' + file_name]

        ret, msg = gnmi_set_with_password([], update_list, [], username, password)
        assert ret != 0, "Auth should fail"
        assert 'Unauthenticated' in msg

    def test_gnmi_set_with_jwt_neg(self):
        path = '/sonic-db:APPL_DB/DASH_QOS'
        value = {
            'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
        }
        update_list = []
        text = json.dumps(value)
        file_name = 'update.txt'
        file_object = open(file_name, 'w')
        file_object.write(text)
        file_object.close()
        update_list = [path + ':@./' + file_name]
    
        token = 'InvalidToken.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.ElsKKULlzGtesThefMuj2_a6KIY9L5i2zDrBLHV-e0M'
        ret, msg = gnmi_set_with_jwt([], update_list, [], token)
        assert ret != 0, "Auth should fail"
        assert 'Unauthenticated' in msg

    def test_gnmi_set_with_jwt(self):
        username = 'gnmitest1'
        password = 'password1'
        ret = add_user(username)
        if ret == False:
            print("Fail to add user, skip this test...")
            return

        ret, msg = gnoi_authenticate(username, password)
        assert ret == 0, msg
        assert 'access_token' in msg
        searchObj = re.search( r'"access_token":"(.*?)"', msg, re.M|re.I)
        if searchObj:
            token = searchObj.group(1)
        else:
            pytest.fail("Fail to find token: %s"%msg)

        path = '/sonic-db:APPL_DB/DASH_QOS'
        value = {
            'qos_02': {'bw': '6000', 'cps': '200', 'flows': '101'}
        }
        update_list = []
        text = json.dumps(value)
        file_name = 'update.txt'
        file_object = open(file_name, 'w')
        file_object.write(text)
        file_object.close()
        update_list = [path + ':@./' + file_name]
        ret, msg = gnmi_set_with_jwt([], update_list, [], token)
        assert ret == 0, msg


@pytest.mark.auth
class TestGNOIAuth:

    def test_gnoi_authenticate(self):
        username = 'gnmitest2'
        password = 'password2'
        ret = add_user(username)
        if ret == False:
            print("Fail to add user, skip this test...")
            return
        ret, msg = gnoi_authenticate(username, password)
        assert ret == 0, msg
        assert 'access_token' in msg
        searchObj = re.search( r'"access_token":"(.*?)"', msg, re.M|re.I)
        if searchObj:
            token = searchObj.group(1)
        else:
            pytest.fail("Fail to find token: %s"%msg)

        ret, msg = gnoi_refresh_with_jwt(token)
        assert ret != 0, msg
        assert "Invalid JWT Token" in msg

