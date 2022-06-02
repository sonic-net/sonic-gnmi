
import os
import subprocess

def run_cmd(cmd):
    res = subprocess.Popen(cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    res.wait()
    if res.returncode:
        msg = str(res.stderr.read(), encoding='utf-8')
    else:
        msg = str(res.stdout.read(), encoding='utf-8')
    return res.returncode, msg

def gnmi_set(delete_list, update_list, replace_list):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_set '
    cmd += '-insecure -username admin -password sonicadmin '
    cmd += '-target_addr 127.0.0.1:8080 '
    cmd += '-alsologtostderr '
    for delete in delete_list:
        cmd += "-delete " + delete
    for update in update_list:
        cmd += "-update " + update
    for replace in replace_list:
        cmd += "-replace " + replace
    return run_cmd(cmd)

class TestGNMIApplDb:

    def test_gnmi_set(self):
        file_object = open('update.txt', 'w')
        file_object.write('{"qos_01": {"bw": "54321", "cps": "1000", "flows": "300"}}')
        file_object.close()
        delete_list = []
        update_list = [
            '/sonic-db:APPL_DB/DASH_QOS:@./update.txt'
        ]
        replace_list = []
        ret, msg = gnmi_set(delete_list, update_list, replace_list)
        assert ret == 0, msg

