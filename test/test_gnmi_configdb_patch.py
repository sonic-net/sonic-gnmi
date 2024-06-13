import os
import json
import jsonpatch
from utils import gnmi_set, gnmi_get, gnmi_dump

import pytest

patch_file = "/tmp/gcu.patch"
config_file = "/tmp/config_db.json.tmp"
checkpoint_file = "/etc/sonic/config.cp.json"

def create_dir(path):
    isExists = os.path.exists(path)
    if not isExists:
        os.makedirs(path)

def create_checkpoint(file_name, text):
    create_dir(os.path.dirname(file_name))
    file_object = open(file_name, "w")
    file_object.write(text)
    file_object.close()
    return

test_data_aaa_patch = [
    {
        "test_name": "aaa_tc1_add_config",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA",
                "value": {
                    "accounting": {
                        "login": "tacacs+,local"
                    },
                    "authentication": {
                        "debug": "True",
                        "failthrough": "True",
                        "fallback": "True",
                        "login": "tacacs+",
                        "trace": "True"
                    },
                    "authorization": {
                        "login": "tacacs+,local"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "AAA": {
                "accounting": {"login": "tacacs+,local"},
                "authentication": {"debug": "True", "failthrough": "True", "fallback": "True", "login": "tacacs+", "trace": "True"},
                "authorization": {"login": "tacacs+,local"}
            }
        }
    },
    {
        "test_name": "aaa_tc1_replace",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA/authorization/login",
                "value": "tacacs+"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA/authentication/login",
                "value": "tacacs+"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA/accounting/login",
                "value": "tacacs+"
            },
        ],
        "origin_json": {
            "AAA": {
                "accounting": {"login": "tacacs+,local"},
                "authentication": {"debug": "True", "failthrough": "True", "fallback": "True", "login": "tacacs+", "trace": "True"},
                "authorization": {"login": "tacacs+,local"}
            }
        },
        "target_json": {
            "AAA": {
                "accounting": {"login": "tacacs+"},
                "authentication": {"debug": "True", "failthrough": "True", "fallback": "True", "login": "tacacs+", "trace": "True"},
                "authorization": {"login": "tacacs+"}
            }
        }
    },
    {
        "test_name": "aaa_tc1_add_duplicate",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA/authorization/login",
                "value": "tacacs+"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA/authorization/login",
                "value": "tacacs+"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA/authorization/login",
                "value": "tacacs+"
            }
        ],
        "origin_json": {
            "AAA": {
                "authorization": {"login": ""}
            }
        },
        "target_json": {
            "AAA": {
                "authorization": {"login": "tacacs+"}
            }
        }
    },
    {
        "test_name": "aaa_tc1_remove",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/AAA",
            }
        ],
        "origin_json": {
            "AAA": {
                "authorization": {"login": ""}
            }
        },
        "target_json": {}
    },
    {
        "test_name": "tacacs_global_tc2_add_config",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/TACPLUS",
                "value": {
                    "global": {
                        "auth_type": "login",
                        "passkey": "testing123",
                        "timeout": "10"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "TACPLUS": {
                "global": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "timeout": "10"
                }
            }
        }
    },
    {
        "test_name": "tacacs_global_tc2_duplicate_input",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/TACPLUS",
                "value": {
                    "global": {
                        "auth_type": "login",
                        "passkey": "testing123",
                        "timeout": "10"
                    }
                }
            }
        ],
        "origin_json": {
            "TACPLUS": {
                "global": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "timeout": "10"
                }
            }
        },
        "target_json": {
            "TACPLUS": {
                "global": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "timeout": "10"
                }
            }
        }
    },
    {
        "test_name": "tacacs_global_tc2_remove",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/TACPLUS"
            }
        ],
        "origin_json": {
            "TACPLUS": {
                "global": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "timeout": "10"
                }
            }
        },
        "target_json": {}
    },
    {
        "test_name": "tacacs_server_tc3_add_init",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/TACPLUS_SERVER",
                "value": {
                    "100.127.20.21": {
                        "auth_type": "login",
                        "passkey": "testing123",
                        "priority": "10",
                        "tcp_port": "50",
                        "timeout": "10"
                    },
                    "fc10::21": {
                        "auth_type": "login",
                        "passkey": "testing123",
                        "priority": "10",
                        "tcp_port": "50",
                        "timeout": "10"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "TACPLUS_SERVER": {
                "100.127.20.21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                },
                "fc10::21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                }
            }
        }
    },
    {
        "test_name": "tacacs_server_tc3_add_duplicate",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/TACPLUS_SERVER/100.127.20.21",
                "value": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                }
            }
        ],
        "origin_json": {
            "TACPLUS_SERVER": {
                "100.127.20.21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                },
                "fc10::21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                }
            }
        },
        "target_json": {
            "TACPLUS_SERVER": {
                "100.127.20.21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                },
                "fc10::21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                }
            }
        }
    },
    {
        "test_name": "tacacs_server_tc3_remove",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/TACPLUS_SERVER"
            }
        ],
        "origin_json": {
            "TACPLUS_SERVER": {
                "100.127.20.21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                },
                "fc10::21": {
                    "auth_type": "login",
                    "passkey": "testing123",
                    "priority": "10",
                    "tcp_port": "50",
                    "timeout": "10"
                }
            }
        },
        "target_json": {}
    }
]

class TestGNMIConfigDbPatch:

    def common_test_handler(self, test_data):
        '''
        Common code for all patch test
        '''
        if os.path.exists(patch_file):
            os.system("rm " + patch_file)
        create_checkpoint(checkpoint_file, json.dumps(test_data['origin_json']))
        update_list = []
        replace_list = []
        delete_list = []
        for i, data in enumerate(test_data["operations"]):
            path = data["path"]
            if data['op'] == "update":
                value = json.dumps(data["value"])
                file_name = "update" + str(i)
                file_object = open(file_name, "w")
                file_object.write(value)
                file_object.close()
                update_list.append(path + ":@./" + file_name)
            elif data['op'] == "replace":
                value = json.dumps(data["value"])
                file_name = "replace" + str(i)
                file_object = open(file_name, "w")
                file_object.write(value)
                file_object.close()
                replace_list.append(path + ":@./" + file_name)
            elif data['op'] == "del":
                delete_list.append(path)
            else:
                pytest.fail("Invalid operation: %s" % data['op'])

        # Send GNMI request
        ret, msg = gnmi_set(delete_list, update_list, replace_list)
        assert ret == 0, msg
        assert os.path.exists(patch_file), "No patch file"
        with open(patch_file,"r") as pf:
            patch_json = json.load(pf)
        # Apply patch to get json result
        result = jsonpatch.apply_patch(test_data["origin_json"], patch_json)
        # Compare json result
        diff = jsonpatch.make_patch(result, test_data["target_json"])
        assert len(diff.patch) == 0, "%s failed, generated json: %s" % (test_data["test_name"], str(result))

    @pytest.mark.parametrize("test_data", test_data_aaa_patch)
    def test_gnmi_aaa_patch(self, test_data):
        '''
        Generate GNMI request for AAA and verify jsonpatch
        '''
        self.common_test_handler(test_data)

