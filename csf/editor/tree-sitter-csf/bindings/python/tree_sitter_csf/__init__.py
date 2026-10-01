"""Tree-sitter language and generated highlight query for CSF architecture."""
from importlib.resources import files
from ._binding import language

HIGHLIGHTS_QUERY = files(__package__).joinpath("highlights.scm").read_text(encoding="utf-8")
__all__ = ["language", "HIGHLIGHTS_QUERY"]
