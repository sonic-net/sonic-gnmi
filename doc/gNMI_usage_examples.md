# gNMI Usage:

These examples show various use case examples using the gnmi_get, gnmi_set and gnmi_cli tools.

The examples use the following open source gNMI CLI tools:

  - [gnmi_get](https://github.com/google/gnxi/tree/master/gnmi_get): This tools allows for retrieval of one or more paths 
  - [gnmi_set](https://github.com/google/gnxi/tree/master/gnmi_get): This tools allows for update, replace and delete operations.
  - [gnmi_cli](https://github.com/openconfig/gnmi/tree/master/cmd/gnmi_cli): This tool allows for dial-in Subscription operations as well as Capabilities request to list available models.

The above tools also have custom patches applied for additional features [here](https://github.com/sonic-net/sonic-gnmi/tree/master/patches). The patched versions of these tools are built with the sonic-telemetry repo and are installed in /usr/bin in the telemetry container.

## Openconfig Models:

These examples show get/set/subscribe/capabilities with the supported openconfig models


### Get:

Gets JSON_IETF values from specified openconfig path.

#### Input:
    gnmi_get -insecure -username admin -password sonicadmin -xpath /openconfig-interfaces:interfaces/interface[name=Ethernet0]/config -target_addr 127.0.0.1:8080 -xpath_target OC-YANG

#### Output:
    == getRequest:
    prefix: <
      target: "OC-YANG"
    >
    path: <
      elem: <
        name: "openconfig-interfaces:interfaces"
      >
      elem: <
        name: "interface"
        key: <
          key: "name"
          value: "Ethernet0"
        >
      >
      elem: <
        name: "config"
      >
    >
    encoding: JSON_IETF

    == getResponse:
    notification: <
      timestamp: 1607105561153639237
      prefix: <
        target: "OC-YANG"
      >
      update: <
        path: <
          elem: <
            name: "openconfig-interfaces:interfaces"
          >
          elem: <
            name: "interface"
            key: <
              key: "name"
              value: "Ethernet0"
            >
          >
          elem: <
            name: "config"
          >
        >
        val: <
          json_ietf_val: "{\"openconfig-interfaces:config\":{\"description\":\"\",\"enabled\":true,\"mtu\":9108,\"name\":\"Ethernet0\",\"type\":\"iana-if-type:ethernetCsmacd\"}}"
        >
      >
    >

### Set:

Sets values using JSON_IETF payload.

#### Input:
    gnmi_set -insecure -username admin -password sonicadmin -update /openconfig-interfaces:interfaces/interface[name=Ethernet0]/config/mtu:@./mtu.json -target_addr localhost:8080 -xpath_target OC-YANG

#### mtu.json:
    {"mtu": 9108}

#### Output:
    == setRequest:
    prefix: <
      target: "OC-YANG"
    >
    update: <
      path: <
        elem: <
          name: "openconfig-interfaces:interfaces"
        >
        elem: <
          name: "interface"
          key: <
            key: "name"
            value: "Ethernet0"
          >
        >
        elem: <
          name: "config"
        >
        elem: <
          name: "mtu"
        >
      >
      val: <
        json_ietf_val: "{\"mtu\": 9108}"
      >
    >

    == setResponse:
    prefix: <
      target: "OC-YANG"
    >
    response: <
      path: <
        elem: <
          name: "openconfig-interfaces:interfaces"
        >
        elem: <
          name: "interface"
          key: <
            key: "name"
            value: "Ethernet0"
          >
        >
        elem: <
          name: "config"
        >
        elem: <
          name: "mtu"
        >
      >
      op: UPDATE
    >


### Capabilities:

Returns list of supported openconfig models and versions as well as supporrted encodings.

#### Input:
    gnmi_cli -insecure -with_user_pass -capabilities -address 127.0.0.1:8080

#### Output:
    supported_models: <
      name: "openconfig-acl"
      organization: "OpenConfig working group"
      version: "1.0.2"
    >
    supported_models: <
      name: "openconfig-system-ext"
      organization: "OpenConfig working group"
      version: "0.10.0"
    >
    supported_models: <
      name: "openconfig-lacp"
      organization: "OpenConfig working group"
      version: "1.1.1"
    >
    supported_models: <
      name: "openconfig-platform"
      organization: "OpenConfig working group"
      version: "0.12.3"
    >
    ...


### Subscribe:

Subscribe to openconfig paths with either streaming, polling or once type subscription.

#### Input:
    gnmi_cli -insecure -logtostderr -address 127.0.0.1:8080 -query_type s -streaming_type TARGET_DEFINED -q /openconfig-interfaces:interfaces/interface[name=Ethernet0]/state/oper-status -target OC-YANG -with_user_pass

#### Output:
    password: {
      "OC-YANG": {
        "openconfig-interfaces:interfaces": {
          "interface": {
            "Ethernet0": {
              "state": {
                "oper-status": "{\"openconfig-interfaces:oper-status\":\"DOWN\"}"
              }
            }
          }
        }
      }
    }
