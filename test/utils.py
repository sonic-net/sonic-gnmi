import os
import re
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
        cmd += " -delete " + delete
    for update in update_list:
        cmd += " -update " + update
    for replace in replace_list:
        cmd += " -replace " + replace
    ret, msg = run_cmd(cmd)
    if ret == 0:
        return ret, ''
    return ret, msg

def gnmi_get(path_list):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_get '
    cmd += '-insecure -username admin -password sonicadmin '
    cmd += '-target_addr 127.0.0.1:8080 '
    cmd += '-alsologtostderr '
    for path in path_list:
        cmd += " -xpath " + path
    ret, msg = run_cmd(cmd)
    if ret == 0:
        msg = msg.replace('\\', '')
        find_list = re.findall( r'json_ietf_val:\s*"(.*?)"\s*>', msg)
        if find_list:
            return ret, find_list
        else:
            return -1, [msg]
    return ret, [msg]

def gnmi_dump(name):
    path = os.getcwd()
    cmd = 'sudo ' + path + '/build/bin/gnmi_dump'
    ret, msg = run_cmd(cmd)
    if ret == 0:
        msg_list = msg.split('\n')
        for line in msg_list:
            if '---' in line:
                current = line.split('---')
                if current[0] == name:
                    return 0, int(current[1])
        return -1, 0
    return ret, 0
