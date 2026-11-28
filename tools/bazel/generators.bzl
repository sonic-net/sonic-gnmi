"""Declared invocation of the owner's existing gNMI YANG plugin."""

def _gnoi_yang_impl(ctx):
    prefix = "/".join([part for part in [
        ctx.bin_dir.path,
        ctx.label.workspace_root,
        ctx.label.package,
        ctx.attr.output_dir,
    ] if part])
    args = ctx.actions.args()
    args.add("--models", ctx.file.models.path)
    args.add("--pyang", ctx.executable.pyang)
    args.add("--plugin", ctx.file.plugin)
    args.add("--output", prefix)
    args.add_all([f.path.removeprefix(prefix + "/") for f in ctx.outputs.outs], before_each = "--expected")
    ctx.actions.run(
        executable = ctx.executable._driver,
        arguments = [args],
        inputs = [ctx.file.models, ctx.file.plugin] + ctx.files.templates,
        tools = [ctx.attr._driver[DefaultInfo].files_to_run, ctx.attr.pyang[DefaultInfo].files_to_run],
        outputs = ctx.outputs.outs,
        env = {"PYTHONHASHSEED": "0", "PYTHONNOUSERSITE": "1"},
        execution_requirements = {"block-network": "1"},
        mnemonic = "GnmiYangProtobuf",
    )
    return [DefaultInfo(files = depset(ctx.outputs.outs))]

gnoi_yang = rule(
    implementation = _gnoi_yang_impl,
    attrs = {
        "models": attr.label(allow_single_file = True, mandatory = True),
        "pyang": attr.label(executable = True, cfg = "exec", mandatory = True),
        "plugin": attr.label(allow_single_file = True, mandatory = True),
        "templates": attr.label_list(allow_files = [".j2"]),
        "output_dir": attr.string(mandatory = True),
        "outs": attr.output_list(mandatory = True),
        "_driver": attr.label(default = "//tools/bazel:generate_yang", executable = True, cfg = "exec"),
    },
)
