from setuptools import Extension, setup
from setuptools.command.build_py import build_py
from pathlib import Path


class BuildWithQuery(build_py):
    def run(self):
        super().run()
        target = Path(self.build_lib) / "tree_sitter_csf" / "highlights.scm"
        target.write_bytes(Path("queries/highlights.scm").read_bytes())


setup(
    packages=["tree_sitter_csf"],
    package_dir={"": "bindings/python"},
    cmdclass={"build_py": BuildWithQuery},
    ext_modules=[Extension(
        "tree_sitter_csf._binding",
        sources=["bindings/python/tree_sitter_csf/binding.c", "src/parser.c", "src/scanner.c"],
        include_dirs=["src"],
        extra_compile_args=["-std=c11"],
    )],
)
