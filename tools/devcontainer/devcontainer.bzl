"""Project the Bazel execution-image owner with the native OCaml generator."""

def _source_label(ctx, label):
    # The shared sources are owned by the public CSF module. Use its stable
    # apparent name from a consumer, never Bazel's internal @@csf+ spelling.
    repository = "" if label.workspace_name == ctx.label.workspace_name else "@csf"
    return repository + "//" + label.package + ":" + label.name

def _devcontainer_impl(ctx):
    output = ctx.actions.declare_file(ctx.attr.output or ctx.label.name + ".Dockerfile")
    if ctx.file.runtime and ctx.file.template:
        fail("runtime and template are mutually exclusive")
    source = ctx.file.runtime or ctx.file.template
    mode = "runtime" if ctx.file.runtime else "template" if ctx.file.template else "public"
    source_labels = [_source_label(ctx, ctx.attr.execution_image.label)]
    if source:
        source_labels.append(_source_label(ctx, (ctx.attr.runtime or ctx.attr.template).label))
    generator = _source_label(ctx, ctx.attr._generator.label).rsplit(":", 1)[0] + ":devcontainer_codegen.ml"
    owner = "//" + ctx.label.package + ":" + ctx.label.name
    ctx.actions.run(
        executable = ctx.executable._generator,
        inputs = [ctx.file.execution_image] + ([source] if source else []),
        outputs = [output],
        arguments = [
            mode,
            ctx.file.execution_image.path,
            source.path if source else "-",
            output.path,
            generator,
            owner,
            ctx.attr.regenerate,
        ] + source_labels,
        mnemonic = "DevcontainerProjection",
    )
    return [DefaultInfo(files = depset([output]))]

devcontainer = rule(
    implementation = _devcontainer_impl,
    attrs = {
        "execution_image": attr.label(
            default = Label("//bazel:execution_image.txt"),
            allow_single_file = True,
        ),
        "output": attr.string(),
        "regenerate": attr.string(default = "bash tools/devcontainer/generate.sh write"),
        "runtime": attr.label(allow_single_file = True),
        "template": attr.label(allow_single_file = True),
        "_generator": attr.label(
            default = Label("//tools/devcontainer:codegen"),
            cfg = "exec",
            executable = True,
        ),
    },
)
