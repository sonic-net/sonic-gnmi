import os
import json
import jsonpatch
from utils import gnmi_set, gnmi_get, gnmi_dump

import pytest

patch_file = "/tmp/gcu.target"
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

test_data_bgp_prefix_patch = [
    {
        "test_name": "bgp_prefix_tc1_add_config",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_ALLOWED_PREFIXES",
                "value": {
                    "DEPLOYMENT_ID|0|1010:1010": {
                        "prefixes_v4": [
                            "10.20.0.0/16"
                        ],
                        "prefixes_v6": [
                            "fc01:20::/64"
                        ]
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "BGP_ALLOWED_PREFIXES": {
                "DEPLOYMENT_ID|0|1010:1010": {
                    "prefixes_v4": [
                        "10.20.0.0/16"
                    ],
                    "prefixes_v6": [
                        "fc01:20::/64"
                    ]
                }
            }
        }
    },
    {
        "test_name": "bgp_prefix_tc1_replace",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_ALLOWED_PREFIXES/DEPLOYMENT_ID\\|0\\|1010:1010/prefixes_v6/0",
                "value": "fc01:30::/64"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_ALLOWED_PREFIXES/DEPLOYMENT_ID\\|0\\|1010:1010/prefixes_v4/0",
                "value": "10.30.0.0/16"
            }
        ],
        "origin_json": {
            "BGP_ALLOWED_PREFIXES": {
                "DEPLOYMENT_ID|0|1010:1010": {
                    "prefixes_v4": [
                        "10.20.0.0/16"
                    ],
                    "prefixes_v6": [
                        "fc01:20::/64"
                    ]
                }
            }
        },
        "target_json": {
            "BGP_ALLOWED_PREFIXES": {
                "DEPLOYMENT_ID|0|1010:1010": {
                    "prefixes_v4": [
                        "10.30.0.0/16"
                    ],
                    "prefixes_v6": [
                        "fc01:30::/64"
                    ]
                }
            }
        }
    },
    {
        "test_name": "bgp_prefix_tc1_add",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_ALLOWED_PREFIXES/DEPLOYMENT_ID\\|0\\|1010:1010/prefixes_v6/0",
                "value": "fc01:30::/64"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_ALLOWED_PREFIXES/DEPLOYMENT_ID\\|0\\|1010:1010/prefixes_v4/0",
                "value": "10.30.0.0/16"
            }
        ],
        "origin_json": {
            "BGP_ALLOWED_PREFIXES": {
                "DEPLOYMENT_ID|0|1010:1010": {
                    "prefixes_v4": [
                        "10.20.0.0/16"
                    ],
                    "prefixes_v6": [
                        "fc01:20::/64"
                    ]
                }
            }
        },
        "target_json": {
            "BGP_ALLOWED_PREFIXES": {
                "DEPLOYMENT_ID|0|1010:1010": {
                    "prefixes_v4": [
                        "10.30.0.0/16", "10.20.0.0/16"
                    ],
                    "prefixes_v6": [
                        "fc01:30::/64", "fc01:20::/64"
                    ]
                }
            }
        }
    },
    {
        "test_name": "bgp_prefix_tc1_remove",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_ALLOWED_PREFIXES"
            }
        ],
        "origin_json": {
            "BGP_ALLOWED_PREFIXES": {
                "DEPLOYMENT_ID|0|1010:1010": {
                    "prefixes_v4": [
                        "10.30.0.0/16", "10.20.0.0/16"
                    ],
                    "prefixes_v6": [
                        "fc01:30::/64", "fc01:20::/64"
                    ]
                }
            }
        },
        "target_json": {}
    }
]

test_data_bgp_sentinel_patch = [
    {
        "test_name": "bgp_sentinel_tc1_add_config",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS",
                "value": {
                    "BGPSentinel": {
                        "ip_range": [
                            "10.10.20.0/24"
                        ],
                        "name": "BGPSentinel",
                        "src_address": "10.5.5.5"
                    },
                    "BGPSentinelV6": {
                        "ip_range": [
                            "2603:10a1:30a:8000::/59"
                        ],
                        "name": "BGPSentinelV6",
                        "src_address": "fc00:fc00:0:10::5"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.5.5.5"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:fc00:0:10::5"
                }
            }
        }
    },
    {
        "test_name": "bgp_sentinel_tc1_add_dummy_ip_range",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS/BGPSentinel/ip_range/1",
                "value": "10.255.0.0/25"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS/BGPSentinelV6/ip_range/1",
                "value": "cc98:2008:2012:2022::/64"
            }
        ],
        "origin_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.5.5.5"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:fc00:0:10::5"
                }
            }
        },
        "target_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24", "10.255.0.0/25"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.5.5.5"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59", "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:fc00:0:10::5"
                }
            }
        }
    },
    {
        "test_name": "bgp_sentinel_tc1_rm_dummy_ip_range",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS/BGPSentinel/ip_range/1",
                "value": "10.255.0.0/25"
            },
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS/BGPSentinelV6/ip_range/1",
                "value": "cc98:2008:2012:2022::/64"
            }
        ],
        "origin_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24", "10.255.0.0/25"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.5.5.5"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59", "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:fc00:0:10::5"
                }
            }
        },
        "target_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.5.5.5"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:fc00:0:10::5"
                }
            }
        }
    },
    {
        "test_name": "bgp_sentinel_tc1_replace_src_address",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS/BGPSentinel/src_address",
                "value": "10.1.0.33"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_SENTINELS/BGPSentinelV6/src_address",
                "value": "fc00:1::33"
            }
        ],
        "origin_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.5.5.5"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:fc00:0:10::5"
                }
            }
        },
        "target_json": {
            "BGP_SENTINELS": {
                "BGPSentinel": {
                    "ip_range": [
                        "10.10.20.0/24"
                    ],
                    "name": "BGPSentinel",
                    "src_address": "10.1.0.33"
                },
                "BGPSentinelV6": {
                    "ip_range": [
                        "2603:10a1:30a:8000::/59"
                    ],
                    "name": "BGPSentinelV6",
                    "src_address": "fc00:1::33"
                }
            }
        }
    }
]

test_data_bgp_speaker_patch = [
    {
        "test_name": "bgp_speaker_tc1_add_config",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE",
                "value": {
                    "BGPSLBPassive": {
                        "ip_range": [
                            "10.255.0.0/25"
                        ],
                        "name": "BGPSLBPassive",
                        "src_address": "10.1.0.33"
                    },
                    "BGPSLBPassiveV6": {
                        "ip_range": [
                            "cc98:2008:2012:2022::/64"
                        ],
                        "name": "BGPSLBPassiveV6",
                        "src_address": "fc00:1::33"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.1.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:1::33"
                }
            }
        }
    },
    {
        "test_name": "bgp_speaker_tc1_add_dummy_ip_range",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE/BGPSLBPassive/ip_range/1",
                "value": "20.255.0.0/25"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE/BGPSLBPassiveV6/ip_range/1",
                "value": "cc98:2008:2012:2222::/64"
            }
        ],
        "origin_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.1.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:1::33"
                }
            }
        },
        "target_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25", "20.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.1.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64", "cc98:2008:2012:2222::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:1::33"
                }
            }
        }
    },
    {
        "test_name": "bgp_speaker_tc1_rm_dummy_ip_range",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE/BGPSLBPassive/ip_range/1"
            },
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE/BGPSLBPassiveV6/ip_range/1"
            }
        ],
        "origin_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25", "20.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.1.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64", "cc98:2008:2012:2222::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:1::33"
                }
            }
        },
        "target_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.1.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:1::33"
                }
            }
        }
    },
    {
        "test_name": "bgp_speaker_tc1_replace_src_address",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE/BGPSLBPassive/src_address",
                "value": "10.2.0.33"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_PEER_RANGE/BGPSLBPassiveV6/src_address",
                "value": "fc00:2::33"
            }
        ],
        "origin_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.1.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:1::33"
                }
            }
        },
        "target_json": {
            "BGP_PEER_RANGE": {
                "BGPSLBPassive": {
                    "ip_range": [
                        "10.255.0.0/25"
                    ],
                    "name": "BGPSLBPassive",
                    "src_address": "10.2.0.33"
                },
                "BGPSLBPassiveV6": {
                    "ip_range": [
                        "cc98:2008:2012:2022::/64"
                    ],
                    "name": "BGPSLBPassiveV6",
                    "src_address": "fc00:2::33"
                }
            }
        }
    }
]

test_data_bgp_mon_patch = [
    {
        "test_name": "bgpmon_tc1_add_init",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_MONITORS",
                "value": {
                    "10.10.10.10": {
                        "admin_status": "up",
                        "asn": "66666",
                        "holdtime": "180",
                        "keepalive": "60",
                        "local_addr": "10.10.10.20",
                        "name": "BGPMonitor",
                        "nhopself": "0",
                        "rrclient": "0"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        }
    },
    {
        "test_name": "bgpmon_tc1_add_duplicate",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_MONITORS/10.10.10.10",
                "value": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        ],
        "origin_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        },
        "target_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        }
    },
    {
        "test_name": "bgpmon_tc1_admin_change",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_MONITORS/10.10.10.10/admin_status",
                "value": "down"
            }
        ],
        "origin_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        },
        "target_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "down",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        }
    },
    {
        "test_name": "bgpmon_tc1_ip_change",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_MONITORS/10.10.10.10",
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_MONITORS/10.10.10.30",
                "value": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        ],
        "origin_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        },
        "target_json": {
            "BGP_MONITORS": {
                "10.10.10.30": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        }
    },
    {
        "test_name": "bgpmon_tc1_remove",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_MONITORS",
            }
        ],
        "origin_json": {
            "BGP_MONITORS": {
                "10.10.10.10": {
                    "admin_status": "up",
                    "asn": "66666",
                    "holdtime": "180",
                    "keepalive": "60",
                    "local_addr": "10.10.10.20",
                    "name": "BGPMonitor",
                    "nhopself": "0",
                    "rrclient": "0"
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
        if os.path.exists(patch_file):
            with open(patch_file,"r") as pf:
                result = json.load(pf)
            # Compare json result
            diff = jsonpatch.make_patch(result, test_data["target_json"])
            assert len(diff.patch) == 0, "%s failed, generated json: %s" % (test_data["test_name"], str(result))
        else:
            # Compare json result
            diff = jsonpatch.make_patch(test_data['origin_json'], test_data["target_json"])
            assert len(diff.patch) == 0, "%s failed, no patch file" % (test_data["test_name"])

    @pytest.mark.parametrize("test_data", test_data_aaa_patch)
    def test_gnmi_aaa_patch(self, test_data):
        '''
        Generate GNMI request for AAA and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_bgp_prefix_patch)
    def test_gnmi_bgp_prefix_patch(self, test_data):
        '''
        Generate GNMI request for BGP prefix and verify jsonpatch
        '''
        self.common_test_handler(test_data)
 
    @pytest.mark.parametrize("test_data", test_data_bgp_sentinel_patch)
    def test_gnmi_bgp_sentinel_patch(self, test_data):
        '''
        Generate GNMI request for BGP sentinel and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_bgp_speaker_patch)
    def test_gnmi_bgp_speaker_patch(self, test_data):
        '''
        Generate GNMI request for BGP speaker and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_bgp_mon_patch)
    def test_gnmi_bgp_mon_patch(self, test_data):
        '''
        Generate GNMI request for BGP monitor and verify jsonpatch
        '''
        self.common_test_handler(test_data)
