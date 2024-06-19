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

test_data_cacl_patch = [
    {
        "test_name": "cacl_tc1_add_new_table",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_TABLE/TEST_1",
                "value": {
                    "policy_desc": "Test_Table_1",
                    "services": [
                        "SNMP"
                    ],
                    "stage": "ingress",
                    "type": "CTRLPLANE"
                }
            }
        ],
        "origin_json": {
            "ACL_TABLE": {}
        },
        "target_json": {
            "ACL_TABLE": {
                "TEST_1": {
                    "policy_desc": "Test_Table_1",
                    "services": [
                        "SNMP"
                    ],
                    "stage": "ingress",
                    "type": "CTRLPLANE"
                }
            }
        }
    },
    {
        "test_name": "cacl_tc1_add_duplicate_table",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_TABLE/SNMP_ACL",
                "value": {
                    "policy_desc": "SNMP_ACL",
                    "services": [
                        "SNMP"
                    ],
                    "stage": "ingress",
                    "type": "CTRLPLANE"
                }
            }
        ],
        "origin_json": {
            "ACL_TABLE": {
                "SNMP_ACL": {
                    "policy_desc": "SNMP_ACL",
                    "services": [
                        "SNMP"
                    ],
                    "stage": "ingress",
                    "type": "CTRLPLANE"
                }
            }
        },
        "target_json": {
            "ACL_TABLE": {
                "SNMP_ACL": {
                    "policy_desc": "SNMP_ACL",
                    "services": [
                        "SNMP"
                    ],
                    "stage": "ingress",
                    "type": "CTRLPLANE"
                }
            }
        }
    },
    {
        "test_name": "cacl_tc1_replace_table_variable",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_TABLE/SNMP_ACL/stage",
                "value": "egress"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_TABLE/SNMP_ACL/services/0",
                "value": "SSH"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_TABLE/SNMP_ACL/policy_desc",
                "value": "SNMP_TO_SSH"
            }
        ],
        "origin_json": {
            "ACL_TABLE": {
                "SNMP_ACL": {
                    "policy_desc": "SNMP_ACL",
                    "services": [
                        "SNMP"
                    ],
                    "stage": "ingress",
                    "type": "CTRLPLANE"
                }
            }
        },
        "target_json": {
            "ACL_TABLE": {
                "SNMP_ACL": {
                    "policy_desc": "SNMP_TO_SSH",
                    "services": [
                        "SSH"
                    ],
                    "stage": "egress",
                    "type": "CTRLPLANE"
                }
            }
        }
    },
    {
        "test_name": "cacl_tc1_remove_table",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_TABLE/SSH_ONLY"
            }
        ],
        "origin_json": {
            "ACL_TABLE": {
                "SNMP_ACL": {
                    "policy_desc": "SNMP_ACL"
                },
                "SSH_ONLY": {
                    "policy_desc": "SSH_ONLY"
                },
                "NTP_ACL": {
                    "policy_desc": "NTP_ACL"
                }
            }
        },
        "target_json": {
            "ACL_TABLE": {
                "SNMP_ACL": {
                    "policy_desc": "SNMP_ACL"
                },
                "NTP_ACL": {
                    "policy_desc": "NTP_ACL"
                }
            }
        }
    }
]

test_data_dhcp_relay_patch = [
    {
        "test_name": "test_dhcp_relay_tc2_add_exist",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/VLAN/Vlan100/dhcp_servers/0",
                "value": "192.0.100.1"
            }
        ],
        "origin_json": {
            "VLAN": {
                "Vlan100": {
                    "dhcp_servers": ["192.1.0.1"]
                }
            }
        },
        "target_json": {
            "VLAN": {
                "Vlan100": {
                    "dhcp_servers": ["192.0.100.1", "192.1.0.1"]
                }
            }
        }
    },
    {
        "test_name": "test_dhcp_relay_tc3_add_and_rm",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/VLAN/Vlan100/dhcp_servers/3"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/VLAN/Vlan100/dhcp_servers/4",
                "value": "192.0.200.5"
            }
        ],
        "origin_json": {
            "VLAN": {
                "Vlan100": {
                    "dhcp_servers": ["192.0.100.1", "192.0.100.2", "192.0.100.3", "192.0.100.4", "192.0.100.5"]
                }
            }
        },
        "target_json": {
            "VLAN": {
                "Vlan100": {
                    "dhcp_servers": ["192.0.100.1", "192.0.100.2", "192.0.100.3", "192.0.100.5", "192.0.200.5"]
                }
            }
        }
    },
    {
        "test_name": "test_dhcp_relay_tc4_replace",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/VLAN/Vlan100/dhcp_servers/0",
                "value": "192.0.100.8"
            }
        ],
        "origin_json": {
            "VLAN": {
                "Vlan100": {
                    "dhcp_servers": ["192.0.100.1", "192.0.100.2", "192.0.100.3", "192.0.100.4", "192.0.100.5"]
                }
            }
        },
        "target_json": {
            "VLAN": {
                "Vlan100": {
                    "dhcp_servers": ["192.0.100.8", "192.0.100.2", "192.0.100.3", "192.0.100.4", "192.0.100.5"]
                }
            }
        }
    }
]

test_data_dynamic_acl_patch = [
    {
        "test_name": "test_gcu_acl_arp_rule_creation",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_RULE",
                "value": {
                    "DYNAMIC_ACL_TABLE|ARP_RULE": {
                        "ETHER_TYPE": "0x0806",
                        "PRIORITY": "9997",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|RULE_3": {
                        "IN_PORTS": "Ethernet4",
                        "PRIORITY": "9995",
                        "PACKET_ACTION": "DROP"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|ARP_RULE": {
                    "ETHER_TYPE": "0x0806",
                    "PRIORITY": "9997",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_dhcp_rule_creation",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_RULE",
                "value": {
                    "DYNAMIC_ACL_TABLE|DHCP_RULE": {
                        "IP_PROTOCOL": "17",
                        "L4_DST_PORT": "67",
                        "ETHER_TYPE": "0x0800",
                        "PRIORITY": "9999",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|DHCPV6_RULE": {
                        "IP_PROTOCOL": "17",
                        "L4_DST_PORT_RANGE": "547-548",
                        "ETHER_TYPE": "0x86DD",
                        "PRIORITY": "9998",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|RULE_3": {
                        "IN_PORTS": "Ethernet4",
                        "PRIORITY": "9995",
                        "PACKET_ACTION": "DROP"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|DHCP_RULE": {
                    "IP_PROTOCOL": "17",
                    "L4_DST_PORT": "67",
                    "ETHER_TYPE": "0x0800",
                    "PRIORITY": "9999",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|DHCPV6_RULE": {
                    "IP_PROTOCOL": "17",
                    "L4_DST_PORT_RANGE": "547-548",
                    "ETHER_TYPE": "0x86DD",
                    "PRIORITY": "9998",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_drop_rule_creation",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_RULE",
                "value": {
                    "DYNAMIC_ACL_TABLE|RULE_3": {
                        "IN_PORTS": "Ethernet4",
                        "PRIORITY": "9995",
                        "PACKET_ACTION": "DROP"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_drop_rule_removal",
        "operations": [
            {
                "op": "del",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_5"
            }
        ],
        "origin_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "PRIORITY": "9997",
                    "PACKET_ACTION": "DROP",
                    "IN_PORTS": "Ethernet4",
                },
                "DYNAMIC_ACL_TABLE|RULE_4": {
                    "PRIORITY": "9996",
                    "PACKET_ACTION": "DROP",
                    "IN_PORTS": "Ethernet8",
                },
                "DYNAMIC_ACL_TABLE|RULE_5": {
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP",
                    "IN_PORTS": "Ethernet12",
                }
            }
        },
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "PRIORITY": "9997",
                    "PACKET_ACTION": "DROP",
                    "IN_PORTS": "Ethernet4",
                },
                "DYNAMIC_ACL_TABLE|RULE_4": {
                    "PRIORITY": "9996",
                    "PACKET_ACTION": "DROP",
                    "IN_PORTS": "Ethernet8",
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_forward_rule_priority_respected",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_RULE",
                "value": {
                    "DYNAMIC_ACL_TABLE|RULE_1": {
                        "DST_IP": "103.23.2.1/32",
                        "PRIORITY": "9999",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|RULE_2": {
                        "DST_IPV6": "103:23:2:1::1/128",
                        "PRIORITY": "9998",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|RULE_3": {
                        "IN_PORTS": "Ethernet4",
                        "PRIORITY": "9995",
                        "PACKET_ACTION": "DROP"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_1": {
                    "DST_IP": "103.23.2.1/32",
                    "PRIORITY": "9999",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_2": {
                    "DST_IPV6": "103:23:2:1::1/128",
                    "PRIORITY": "9998",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_forward_rule_replacement",
        "operations": [
            {
                "op": "del",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_1"
            },
            {
                "op": "del",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_2"
            },
            {
                "op": "update",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_1",
                "value": {
                    "DST_IP": "103.23.2.2/32",
                    "PRIORITY": "9999",
                    "PACKET_ACTION": "FORWARD"
                }
            },
            {
                "op": "update",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_2",
                "value": {
                    "DST_IPV6": "103:23:2:2::1/128",
                    "PRIORITY": "9998",
                    "PACKET_ACTION": "FORWARD"
                }
            }
        ],
        "origin_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_1": {
                    "DST_IP": "103.23.2.1/32",
                    "PRIORITY": "9999",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_2": {
                    "DST_IPV6": "103:23:2:1::1/128",
                    "PRIORITY": "9998",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        },
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_1": {
                    "DST_IP": "103.23.2.2/32",
                    "PRIORITY": "9999",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_2": {
                    "DST_IPV6": "103:23:2:2::1/128",
                    "PRIORITY": "9998",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_forward_rule_removal",
        "operations": [
            {
                "op": "del",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_1"
            },
            {
                "op": "del",
                "path": r"/sonic-db:CONFIG_DB/localhost/ACL_RULE/DYNAMIC_ACL_TABLE\|RULE_2"
            }
        ],
        "origin_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_1": {
                    "DST_IP": "103.23.2.1/32",
                    "PRIORITY": "9999",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_2": {
                    "DST_IPV6": "103:23:2:1::1/128",
                    "PRIORITY": "9998",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        },
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|RULE_3": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9995",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    },
    {
        "test_name": "test_gcu_acl_scale_rules",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/ACL_RULE",
                "value": {
                    "DYNAMIC_ACL_TABLE|FORWARD_RULE_1": {
                        "DST_IP": "103.23.4.1/32",
                        "PRIORITY": "9900",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|FORWARD_RULE_2": {
                        "DST_IPV6": "103.23.4.2/32",
                        "PRIORITY": "9900",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|FORWARD_RULE_3": {
                        "DST_IPV6": "103.23.4.3/32",
                        "PRIORITY": "9900",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|V6_FORWARD_RULE_1": {
                        "DST_IP": "103:23:4:1::1/128",
                        "PRIORITY": "9900",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|V6_FORWARD_RULE_2": {
                        "DST_IP": "103:23:4:2::1/128",
                        "PRIORITY": "9900",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|V6_FORWARD_RULE_3": {
                        "DST_IP": "103:23:4:3::1/128",
                        "PRIORITY": "9900",
                        "PACKET_ACTION": "FORWARD"
                    },
                    "DYNAMIC_ACL_TABLE|DROP_RULE": {
                        "IN_PORTS": "Ethernet4",
                        "PRIORITY": "9000",
                        "PACKET_ACTION": "DROP"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "ACL_RULE": {
                "DYNAMIC_ACL_TABLE|FORWARD_RULE_1": {
                    "DST_IP": "103.23.4.1/32",
                    "PRIORITY": "9900",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|FORWARD_RULE_2": {
                    "DST_IPV6": "103.23.4.2/32",
                    "PRIORITY": "9900",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|FORWARD_RULE_3": {
                    "DST_IPV6": "103.23.4.3/32",
                    "PRIORITY": "9900",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|V6_FORWARD_RULE_1": {
                    "DST_IP": "103:23:4:1::1/128",
                    "PRIORITY": "9900",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|V6_FORWARD_RULE_2": {
                    "DST_IP": "103:23:4:2::1/128",
                    "PRIORITY": "9900",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|V6_FORWARD_RULE_3": {
                    "DST_IP": "103:23:4:3::1/128",
                    "PRIORITY": "9900",
                    "PACKET_ACTION": "FORWARD"
                },
                "DYNAMIC_ACL_TABLE|DROP_RULE": {
                    "IN_PORTS": "Ethernet4",
                    "PRIORITY": "9000",
                    "PACKET_ACTION": "DROP"
                }
            }
        }
    }
]

test_data_ecn_config_patch = [
    {
        "test_name": "test_ecn_config_updates",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/WRED_PROFILE/AZURE_LOSSLESS/green_min_threshold",
                "value": "2000001"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/WRED_PROFILE/AZURE_LOSSLESS/green_max_threshold",
                "value": "10000001"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/WRED_PROFILE/AZURE_LOSSLESS/green_drop_probability",
                "value": "6"
            },
        ],
        "origin_json": {
            "WRED_PROFILE": {
                "AZURE_LOSSLESS": {
                    "green_min_threshold": "2000000",
                    "green_max_threshold": "10000000",
                    "green_drop_probability": "5"
                }
            }
        },
        "target_json": {
            "WRED_PROFILE": {
                "AZURE_LOSSLESS": {
                    "green_min_threshold": "2000001",
                    "green_max_threshold": "10000001",
                    "green_drop_probability": "6"
                }
            }
        }
    }
]

test_data_eth_interface_patch = [
    {
        "test_name": "test_replace_lanes",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/lanes",
                "value": "1,2,3,5"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "lanes": "1,2,3,4"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "lanes": "1,2,3,5"
                }
            }
        }
    },
    {
        "test_name": "test_replace_mtu",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/mtu",
                "value": "1514"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "mtu": "1500"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "mtu": "1514"
                }
            }
        }
    },
    {
        "test_name": "test_toggle_pfc_asym",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/pfc_asym",
                "value": "off"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "pfc_asym": "on"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "pfc_asym": "off"
                }
            }
        }
    },
    {
        "test_name": "test_replace_fec",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/fec",
                "value": "rs"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "fec": "fc"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "fec": "rs"
                }
            }
        }
    },
    {
        "test_name": "test_update_valid_index",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/index",
                "value": "2"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet4/index",
                "value": "1"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "index": "1"
                },
                "Ethernet4": {
                    "index": "2"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "index": "2"
                },
                "Ethernet4": {
                    "index": "1"
                }
            }
        }
    },
    {
        "test_name": "test_update_speed",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/speed",
                "value": "2000"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "speed": "1000"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "speed": "2000"
                }
            }
        }
    },
    {
        "test_name": "test_update_description",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/description",
                "value": "Updated description"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "description": ""
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "description": "Updated description"
                }
            }
        }
    },
    {
        "test_name": "test_eth_interface_admin_change",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/PORT/Ethernet0/admin_status",
                "value": "down"
            }
        ],
        "origin_json": {
            "PORT": {
                "Ethernet0": {
                    "admin_status": "up"
                }
            }
        },
        "target_json": {
            "PORT": {
                "Ethernet0": {
                    "admin_status": "down"
                }
            }
        }
    }
]

test_data_incremental_qos_patch = [
    {
        "test_name": "test_incremental_qos_config_updates_add",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/ingress_lossless_pool/xoff",
                "value": "2000"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/ingress_lossless_pool/size",
                "value": "5000"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/egress_lossy_pool/size",
                "value": "6000"
            }
        ],
        "origin_json": {
            "BUFFER_POOL": {
                "ingress_lossless_pool": {},
                "egress_lossy_pool": {}
            }
        },
        "target_json": {
            "BUFFER_POOL": {
                "ingress_lossless_pool": {
                    "xoff": "2000",
                    "size": "5000"
                },
                "egress_lossy_pool": {
                    "size": "6000"
                }
            }
        }
    },
    {
        "test_name": "test_incremental_qos_config_updates_replace",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/ingress_lossless_pool/xoff",
                "value": "2001"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/ingress_lossless_pool/size",
                "value": "5001"
            },
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/egress_lossy_pool/size",
                "value": "6001"
            }
        ],
        "origin_json": {
            "BUFFER_POOL": {
                "ingress_lossless_pool": {
                    "xoff": "2000",
                    "size": "5000"
                },
                "egress_lossy_pool": {
                    "size": "6000"
                }
            }
        },
        "target_json": {
            "BUFFER_POOL": {
                "ingress_lossless_pool": {
                    "xoff": "2001",
                    "size": "5001"
                },
                "egress_lossy_pool": {
                    "size": "6001"
                }
            }
        }
    },
    {
        "test_name": "test_incremental_qos_config_updates_remove",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/ingress_lossless_pool/xoff"
            },
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/ingress_lossless_pool/size"
            },
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BUFFER_POOL/egress_lossy_pool/size"
            }
        ],
        "origin_json": {
            "BUFFER_POOL": {
                "ingress_lossless_pool": {
                    "xoff": "2000",
                    "size": "5000"
                },
                "egress_lossy_pool": {
                    "size": "6000"
                }
            }
        },
        "target_json": {
            "BUFFER_POOL": {
                "ingress_lossless_pool": {},
                "egress_lossy_pool": {}
            }
        }
    }
]

test_data_ipv6_patch = [
    {
        "test_name": "add_deleted_ipv6_neighbor",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_NEIGHBOR/fc00::7a",
                "value": {
                    "admin_status": "up",
                    "asn": "64600",
                    "holdtime": "10",
                    "keepalive": "3",
                    "local_addr": "fc00::79",
                    "name": "ARISTA03T1",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        ],
        "origin_json": {
            "BGP_NEIGHBOR": {}
        },
        "target_json": {
            "BGP_NEIGHBOR": {
                "fc00::7a": {
                    "admin_status": "up",
                    "asn": "64600",
                    "holdtime": "10",
                    "keepalive": "3",
                    "local_addr": "fc00::79",
                    "name": "ARISTA03T1",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        }
    },
    {
        "test_name": "ipv6_neighbor_admin_change",
        "operations": [
            {
                "op": "replace",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_NEIGHBOR/fc00::7a/admin_status",
                "value": "down"
            }
        ],
        "origin_json": {
            "BGP_NEIGHBOR": {
                "fc00::7a": {
                    "admin_status": "up",
                    "asn": "64600",
                    "holdtime": "10",
                    "keepalive": "3",
                    "local_addr": "fc00::79",
                    "name": "ARISTA03T1",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        },
        "target_json": {
            "BGP_NEIGHBOR": {
                "fc00::7a": {
                    "admin_status": "down",
                    "asn": "64600",
                    "holdtime": "10",
                    "keepalive": "3",
                    "local_addr": "fc00::79",
                    "name": "ARISTA03T1",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        }
    },
    {
        "test_name": "delete_ipv6_neighbor",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/BGP_NEIGHBOR/fc00::7a"
            }
        ],
        "origin_json": {
            "BGP_NEIGHBOR": {
                "fc00::7a": {
                    "admin_status": "up",
                    "asn": "64600",
                    "holdtime": "10",
                    "keepalive": "3",
                    "local_addr": "fc00::79",
                    "name": "ARISTA03T1",
                    "nhopself": "0",
                    "rrclient": "0"
                }
            }
        },
        "target_json": {
            "BGP_NEIGHBOR": {}
        }
    }
]

test_data_k8s_config_patch = [
    {
        "test_name": "K8SEMPTYTOHALFPATCH",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER",
                "value": {"SERVER": {}}
            }
        ],
        "origin_json": {
            "KUBERNETES_MASTER": {}
        },
        "target_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {}
            }
        }
    },
    {
        "test_name": "K8SHALFTOFULLPATCH",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER/SERVER/disable",
                "value": "false"
            },
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER/SERVER/ip",
                "value": "k8svip.ap.gbl"
            }
        ],
        "origin_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {
                    "disable": "true",
                    "ip": ""
                }
            }
        },
        "target_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {
                    "disable": "false",
                    "ip": "k8svip.ap.gbl"
                }
            }
        }
    },
    {
        "test_name": "K8SFULLTOHALFPATCH",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER/SERVER/disable"
            },
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER/SERVER/ip"
            }
        ],
        "origin_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {
                    "disable": "true",
                    "ip": ""
                }
            }
        },
        "target_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {}
            }
        }
    },
    {
        "test_name": "K8SHALFTOEMPTYPATCH",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER"
            }
        ],
        "origin_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {
                    "disable": "true",
                    "ip": ""
                }
            }
        },
        "target_json": {}
    },
    {
        "test_name": "K8SHALFTOEMPTYPATCH",
        "operations": [
            {
                "op": "del",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER"
            }
        ],
        "origin_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {
                    "disable": "true",
                    "ip": ""
                }
            }
        },
        "target_json": {}
    },
    {
        "test_name": "K8SEMPTYTOFULLPATCH",
        "operations": [
            {
                "op": "update",
                "path": "/sonic-db:CONFIG_DB/localhost/KUBERNETES_MASTER",
                "value": {
                    "SERVER": {
                        "disable": "false",
                        "ip": "k8svip.ap.gbl"
                    }
                }
            }
        ],
        "origin_json": {},
        "target_json": {
            "KUBERNETES_MASTER": {
                "SERVER": {
                    "disable": "false",
                    "ip": "k8svip.ap.gbl"
                }
            }
        }
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

    @pytest.mark.parametrize("test_data", test_data_cacl_patch)
    def test_gnmi_cacl_patch(self, test_data):
        '''
        Generate GNMI request for CACL and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_dhcp_relay_patch)
    def test_gnmi_dhcp_relay_patch(self, test_data):
        '''
        Generate GNMI request for dhcp relay and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_dynamic_acl_patch)
    def test_gnmi_dynamic_acl_patch(self, test_data):
        '''
        Generate GNMI request for dynamic acl and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_ecn_config_patch)
    def test_gnmi_ecn_config_patch(self, test_data):
        '''
        Generate GNMI request for ecn config and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_eth_interface_patch)
    def test_gnmi_eth_interface_patch(self, test_data):
        '''
        Generate GNMI request for eth interface and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_incremental_qos_patch)
    def test_gnmi_incremental_qos_patch(self, test_data):
        '''
        Generate GNMI request for incremental qos and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_ipv6_patch)
    def test_gnmi_ipv6_patch(self, test_data):
        '''
        Generate GNMI request for ipv6 and verify jsonpatch
        '''
        self.common_test_handler(test_data)

    @pytest.mark.parametrize("test_data", test_data_k8s_config_patch)
    def test_gnmi_k8s_config_patch(self, test_data):
        '''
        Generate GNMI request for k8s config and verify jsonpatch
        '''
        self.common_test_handler(test_data)
