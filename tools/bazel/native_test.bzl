"""Run original Go suites with private declared SONiC services and fixtures."""

load("@io_bazel_rules_go//go:def.bzl", "go_test")
load("@rules_python//python:py_test.bzl", "py_test")

def _test_compile_settings_impl(_settings, _attr):
    # Make's -gcflags=all=-l is needed throughout gomonkey's test graph.
    # Keep it off the production binaries used by installed-artifact tests.
    return {"@io_bazel_rules_go//go/config:gc_goopts": ["-l"]}

_test_compile_settings = transition(
    implementation = _test_compile_settings_impl,
    inputs = [],
    outputs = ["@io_bazel_rules_go//go/config:gc_goopts"],
)

def _test_executable_impl(ctx):
    binary = ctx.attr.binary[0][DefaultInfo]
    executable = ctx.actions.declare_file(ctx.label.name)
    ctx.actions.symlink(
        output = executable,
        target_file = binary.files_to_run.executable,
        is_executable = True,
    )
    return [DefaultInfo(
        executable = executable,
        runfiles = ctx.runfiles(files = [binary.files_to_run.executable]).merge(binary.default_runfiles),
    )]

_test_executable = rule(
    implementation = _test_executable_impl,
    executable = True,
    attrs = {
        "binary": attr.label(cfg = _test_compile_settings),
        "_allowlist_function_transition": attr.label(
            default = "@bazel_tools//tools/allowlists/function_transition_allowlist",
        ),
    },
)

def _inputs(binary):
    return {
        "binary": binary,
        "runtime": "//tools/bazel:test_runtime",
        "common_helper": "@sonic_swss_common//tools/bazel/runtime:run_with_runtime.py",
        "models": "@sonic_mgmt_common//dist:runtime",
        "schema": "@sonic_mgmt_common//dist:schema",
        "initializer": "@sonic_mgmt_common//tools/test:dbinit",
        "platform": "@sonic_mgmt_common//tools/test:platform.json",
        "anchor": "//:testdata/database_config.json",
    }

def native_go_test(name, **kwargs):
    fixtures = kwargs.pop("data", [])

    # Preserve Make's race-detector coverage (dialout is its explicit exception).
    go_test(
        name = name + "_binary",
        tags = ["manual"],
        data = fixtures,
        gotags = ["gnmi_translib_write", "gnmi_native_write"],
        race = "off" if native.package_name() == "dialout/dialout_client" else "on",
        # Match native go test's default executable mode. Do not let GCC's
        # default PIE layout overlap the race detector's shadow-memory range.
        gc_linkopts = kwargs.pop("gc_linkopts", []) + ["-extldflags", "-no-pie"],
        **kwargs
    )
    _test_executable(
        name = name + "_executable",
        testonly = True,
        tags = ["manual"],
        binary = ":" + name + "_binary",
    )
    inputs = _inputs(":" + name + "_executable")
    if native.package_name() == "gnmi_server":
        inputs.update({
            "pytest": "//tools/bazel:pytest",
            "clients": "//dist:runtime",
        })
    py_test(
        name = name,
        srcs = ["//tools/bazel:run_go_tests.py"],
        main = "//tools/bazel:run_go_tests.py",
        size = "large",
        timeout = "eternal",
        # These original suites share fixed gRPC or ZMQ test ports. Keep Make's
        # serial execution contract even in a multi-job local test run.
        tags = ["exclusive"] if native.package_name() in ["gnmi_server", "dialout/dialout_client", "telemetry", "sonic_data_client"] else [],
        args = ["--suite=" + native.package_name()] +
               ["--" + key + "=$(location " + value + ")" for key, value in inputs.items()] +
               ["--fixtures", "$(rootpaths //:test_data)"],
        data = inputs.values() + fixtures + ["//:test_data"],
    )

def installed_runtime_test(name):
    inputs = _inputs("//telemetry")
    inputs["clients"] = "//dist:runtime"
    py_test(
        name = name,
        srcs = ["//tools/bazel:run_go_tests.py", "//tools/bazel:installed_probe.py"],
        main = "//tools/bazel:run_go_tests.py",
        size = "large",
        args = ["--suite=installed", "--installed"] +
               ["--" + key + "=$(location " + value + ")" for key, value in inputs.items()] +
               ["--fixtures", "$(rootpaths //:test_data)"],
        data = inputs.values() + ["//:test_data"],
    )
