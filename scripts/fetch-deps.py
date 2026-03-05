#!/usr/bin/env python3
"""
fetch-deps.py - Download sonic-gnmi dev container dependencies.

Mirrors what DownloadPipelineArtifact@2 does in azure-pipelines.yml:
  - libyang 1.0.73 + libnl  from pipeline 465 (sonic-buildimage.common_libs)
  - swsscommon               from pipeline 9   (Azure.sonic-swss-common)
  - sonic_yang_models wheel  built from sonic-buildimage source

Usage: python3 scripts/fetch-deps.py <output-dir>
"""

import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import zipfile

import requests

ADO_BASE = "https://dev.azure.com/mssonic/build/_apis/build"

COMMON_LIB_FILES = [
    "target/debs/bookworm/libyang_1.0.73_amd64.deb",
    "target/debs/bookworm/libyang-dev_1.0.73_amd64.deb",
    "target/debs/bookworm/libnl-3-200_3.7.0-0.2+b1sonic1_amd64.deb",
    "target/debs/bookworm/libnl-genl-3-200_3.7.0-0.2+b1sonic1_amd64.deb",
    "target/debs/bookworm/libnl-route-3-200_3.7.0-0.2+b1sonic1_amd64.deb",
    "target/debs/bookworm/libnl-nf-3-200_3.7.0-0.2+b1sonic1_amd64.deb",
]

SWSS_FILES = [
    "sonic-swss-common-bookworm/libswsscommon_1.0.0_amd64.deb",
    "sonic-swss-common-bookworm/libswsscommon-dev_1.0.0_amd64.deb",
    "sonic-swss-common-bookworm/python3-swsscommon_1.0.0_amd64.deb",
]


def ado_get(path):
    url = f"{ADO_BASE}/{path}"
    resp = requests.get(url, timeout=30)
    resp.raise_for_status()
    return resp.json()


def latest_build_id(pipeline_id, branch="master"):
    data = ado_get(
        f"builds?definitions={pipeline_id}&resultFilter=succeeded"
        f"&branchName=refs/heads/{branch}&$top=1&api-version=7.1"
    )
    build_id = data["value"][0]["id"]
    print(f"  Pipeline {pipeline_id}: latest build = {build_id}")
    return build_id


def artifact_download_url(build_id, artifact_name):
    data = ado_get(f"builds/{build_id}/artifacts?artifactName={artifact_name}&api-version=7.1")
    return data["resource"]["downloadUrl"]


def download_zip(url, label):
    print(f"  Downloading {label} ...")
    resp = requests.get(url, timeout=300, stream=True)
    resp.raise_for_status()
    buf = io.BytesIO()
    for chunk in resp.iter_content(chunk_size=8192):
        buf.write(chunk)
    buf.seek(0)
    return buf


def extract_files(zip_bytes, file_list, out_dir):
    with zipfile.ZipFile(zip_bytes) as z:
        for path in file_list:
            basename = os.path.basename(path)
            # Prefer exact match, fall back to prefix match (handles version wildcards)
            matches = [n for n in z.namelist()
                       if os.path.basename(n) == basename or
                       (basename.endswith("_amd64.deb") and
                        os.path.basename(n).startswith(basename.split("_")[0]) and
                        n.endswith(".deb") and
                        "dbgsym" not in n and ".log" not in n)]
            exact = [n for n in matches if os.path.basename(n) == basename]
            target = (exact or matches)[0] if (exact or matches) else None
            if not target:
                print(f"  WARNING: {basename} not found in artifact")
                continue
            dest = os.path.join(out_dir, os.path.basename(target))
            with z.open(target) as src, open(dest, "wb") as dst:
                dst.write(src.read())
            print(f"  + {os.path.basename(target)}")


def build_sonic_yang_models(out_dir):
    print("  Building sonic_yang_models wheel from source ...")
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run([
            "git", "clone", "--depth=1", "--filter=blob:none", "--sparse",
            "https://github.com/sonic-net/sonic-buildimage.git", tmp
        ], check=True, capture_output=True)
        subprocess.run(
            ["git", "sparse-checkout", "set", "src/sonic-yang-models"],
            cwd=tmp, check=True, capture_output=True
        )
        subprocess.run(["git", "checkout"], cwd=tmp, check=True, capture_output=True)
        src = os.path.join(tmp, "src", "sonic-yang-models")
        subprocess.run(
            [sys.executable, "setup.py", "bdist_wheel"],
            cwd=src, check=True, capture_output=True
        )
        for whl in os.listdir(os.path.join(src, "dist")):
            if whl.endswith(".whl"):
                shutil.copy(os.path.join(src, "dist", whl), os.path.join(out_dir, whl))
                print(f"  + {whl}")


def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} <output-dir>")
        sys.exit(1)

    out_dir = sys.argv[1]
    os.makedirs(out_dir, exist_ok=True)

    print("── libyang + libnl (pipeline 465: sonic-buildimage.common_libs) ──")
    build_id = latest_build_id(465)
    url = artifact_download_url(build_id, "common-lib")
    zip_data = download_zip(url, "common-lib")
    extract_files(zip_data, COMMON_LIB_FILES, out_dir)

    print("── swsscommon (pipeline 9: Azure.sonic-swss-common) ──")
    build_id = latest_build_id(9)
    url = artifact_download_url(build_id, "sonic-swss-common-bookworm")
    zip_data = download_zip(url, "sonic-swss-common-bookworm")
    extract_files(zip_data, SWSS_FILES, out_dir)

    print("── sonic_yang_models wheel ──")
    build_sonic_yang_models(out_dir)

    print(f"\nAll deps in {out_dir}:")
    for f in sorted(os.listdir(out_dir)):
        print(f"  {f}")


if __name__ == "__main__":
    main()
