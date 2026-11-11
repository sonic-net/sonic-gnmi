"""Protocol-level ports of sonic-mgmt #28970 and #28267; no server imports."""
import hashlib
import logging
import uuid

import grpc
import pytest
from file import file_pb2, file_pb2_grpc
from gnoi_types import types_pb2
from pygnmi.spec.v080 import gnmi_pb2_grpc

from conftest import CASE_CN, get_request, response_values

LOG = logging.getLogger(__name__)


@pytest.mark.parametrize("role", ["gnmi_config_db_readonly", "gnmi_config_db_readwrite"], ids=["readonly", "readwrite"])
def test_configdb_get(case, role):
    case.db.hset(f"GNMI_CLIENT_CERT|{CASE_CN}", "role@", role)
    assert case.db.hget(f"GNMI_CLIENT_CERT|{CASE_CN}", "role@") == role
    with case.channel() as channel:
        result = gnmi_pb2_grpc.gNMIStub(channel).Get(get_request(), timeout=10)
    assert response_values(result) == ["Private"]
    LOG.info("Get role=%s actual=OK value=Private", role)


def test_unmapped_cn(case):
    assert not case.db.exists(f"GNMI_CLIENT_CERT|{CASE_CN}")
    with case.channel() as channel:
        with pytest.raises(grpc.RpcError) as error:
            gnmi_pb2_grpc.gNMIStub(channel).Get(get_request(), timeout=10)
    assert error.value.code() == grpc.StatusCode.UNAUTHENTICATED
    LOG.info("Get unmapped valid-cert CN actual=UNAUTHENTICATED")


@pytest.mark.parametrize("method", ["Put", "Remove"])
@pytest.mark.parametrize("role,allowed", [("gnoi_readonly", False), ("gnoi_readwrite", True)], ids=["readonly", "readwrite"])
def test_file_write(case, method, role, allowed):
    key = f"GNMI_CLIENT_CERT|{CASE_CN}"
    case.db.hset(key, "role@", role)
    assert case.db.hget(key, "role@") == role
    name = f"auth-{uuid.uuid4().hex}"
    actual = case.files / name
    original, replacement = b"original sentinel\n", b"authorized replacement\n"
    actual.write_bytes(original)
    before_names = set(case.files.iterdir())
    logical = "/tmp/" + name
    try:
        with case.channel() as channel:
            stub = file_pb2_grpc.FileStub(channel)
            if method == "Put":
                messages = [
                    file_pb2.PutRequest(open=file_pb2.PutRequest.Details(remote_file=logical, permissions=0o644)),
                    file_pb2.PutRequest(contents=replacement),
                    file_pb2.PutRequest(hash=types_pb2.HashType(method=types_pb2.HashType.MD5,
                                                              hash=hashlib.md5(replacement).digest())),
                ]
                call = lambda: stub.Put(iter(messages), timeout=10)
            else:
                call = lambda: stub.Remove(file_pb2.RemoveRequest(remote_file=logical), timeout=10)
            if allowed:
                call()
            else:
                with pytest.raises(grpc.RpcError) as error:
                    call()
                assert error.value.code() == grpc.StatusCode.PERMISSION_DENIED
                assert role in error.value.details()
        if not allowed:
            assert actual.read_bytes() == original
            assert set(case.files.iterdir()) == before_names, "Denied RPC created an extra file"
        elif method == "Put":
            assert actual.read_bytes() == replacement
        else:
            assert not actual.exists()
        LOG.info("File.%s role=%s actual=%s effect=%s", method, role,
                 "OK" if allowed else "PERMISSION_DENIED", "changed" if allowed else "unchanged")
    finally:
        actual.unlink(missing_ok=True)
        assert not actual.exists()
