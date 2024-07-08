import os
import re
import subprocess

def run_cmd(cmd):
    res = subprocess.Popen(cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    res.wait()
    msg = str(res.stderr.read(), encoding='utf-8') + str(res.stdout.read(), encoding='utf-8')
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

def gnmi_set_with_password(delete_list, update_list, replace_list, user, password):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_set '
    cmd += '-insecure -username %s -password %s '%(user, password)
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

def gnmi_set_with_jwt(delete_list, update_list, replace_list, token):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_set '
    cmd += '-insecure -jwt_token ' + token + ' '
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

def gnmi_get_with_encoding(path_list, encoding):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_get '
    cmd += '-insecure -username admin -password sonicadmin '
    cmd += '-target_addr 127.0.0.1:8080 '
    cmd += '-alsologtostderr '
    cmd += '-encoding %s '%(encoding)
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

def gnmi_get_proto(path_list, file_list):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_get '
    cmd += '-insecure -username admin -password sonicadmin '
    cmd += '-target_addr 127.0.0.1:8080 '
    cmd += '-alsologtostderr '
    cmd += '-encoding PROTO '
    for path in path_list:
        cmd += " -xpath " + path
    for file in file_list:
        cmd += " -proto_file " + file
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnmi_get_with_password(path_list, user, password):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_get '
    cmd += '-insecure -username %s -password %s '%(user, password)
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

def gnmi_get_with_jwt(path_list, token):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_get '
    cmd += '-insecure -jwt_token ' + token + ' '
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

def gnmi_capabilities():
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_cli '
    cmd += '-client_types=gnmi -a 127.0.0.1:8080 -logtostderr -insecure '
    cmd += '-capabilities '
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnmi_subscribe_poll(gnmi_path, interval, count, timeout):
    path = os.getcwd()
    cmd = path + '/build/bin/gnmi_cli '
    cmd += '-client_types=gnmi -a 127.0.0.1:8080 -logtostderr -insecure '
    # Use sonic-db as default origin
    cmd += '-origin=sonic-db '
    if timeout:
        cmd += '-streaming_timeout=10 '
    cmd += '-query_type=polling '
    cmd += '-polling_interval %us -count %u ' % (interval, count)
    cmd += '-q %s' % (gnmi_path)
    ret, msg = run_cmd(cmd)
    return ret, msg

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

def gnoi_time():
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc Time '
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_reboot(method, delay, message):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc Reboot '
    cmd += '-jsonin "{\\\"method\\\":%d, \\\"delay\\\":%d, \\\"message\\\":\\\"%s\\\"}"'%(method, delay, message)
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_kill_process(json_data):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc KillProcess '
    cmd += f'-jsonin \'{json_data}\''
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_restart_process(json_data):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc KillProcess '
    cmd += f'-jsonin \'{json_data}\''
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_rebootstatus():
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc RebootStatus '
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_cancelreboot(message):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc CancelReboot '
    cmd += '-jsonin "{\\\"message\\\":\\\"%s\\\"}"'%(message)
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_ping(dst):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc Ping '
    cmd += '-jsonin "{\\\"destination\\\":\\\"%s\\\"}"'%(dst)
    ret, msg = run_cmd(cmd)
    return ret, msg


def gnoi_traceroute(dst):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc Traceroute '
    cmd += '-jsonin "{\\\"destination\\\":\\\"%s\\\"}"'%(dst)
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_setpackage():
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc SetPackage '
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_switchcontrolprocessor():
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-rpc SwitchControlProcessor '
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_authenticate(username, password):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-module Sonic -rpc authenticate '
    cmd += '-jsonin "{\\\"Username\\\":\\\"%s\\\", \\\"Password\\\":\\\"%s\\\"}"'%(username, password)
    ret, msg = run_cmd(cmd)
    return ret, msg

def gnoi_refresh_with_jwt(token):
    path = os.getcwd()
    cmd = path + '/build/bin/gnoi_client '
    cmd += '-insecure -target 127.0.0.1:8080 '
    cmd += '-jwt_token ' + token + ' '
    cmd += '-module Sonic -rpc refresh '
    ret, msg = run_cmd(cmd)
    return ret, msg

