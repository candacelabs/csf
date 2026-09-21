import json
import pytest
from google.protobuf import descriptor_pb2

from generate_api import schema_for_mcp, validate_get_request, validate_operation_messages


def method(name, request, response):
    return descriptor_pb2.MethodDescriptorProto(name=name, input_type="." + request, output_type="." + response)


def test_descriptor_lint_accepts_named_pair():
    validate_operation_messages(descriptor_pb2.ServiceDescriptorProto(method=[method("Compile", "x.CompileRequest", "x.CompileResponse")]))


def test_descriptor_lint_rejects_domain_messages():
    with pytest.raises(ValueError, match="operation-specific"):
        validate_operation_messages(descriptor_pb2.ServiceDescriptorProto(method=[method("Compile", "x.Controller", "x.Program")]))


def test_get_request_must_be_empty():
    message = descriptor_pb2.DescriptorProto(name="GetSnapshotRequest", field=[descriptor_pb2.FieldDescriptorProto(name="ignored")])
    with pytest.raises(ValueError, match="empty request"):
        validate_get_request("/api/snapshot", "GetSnapshotRequest", message)

def test_mcp_schema_retains_transitive_recursive_definitions_only():
    components = {
        "Request": {"properties": {"node": {"$ref": "#/components/schemas/Node"}}},
        "Node": {"properties": {"next": {"$ref": "#/components/schemas/Node"}}},
        "Unrelated": {"type": "string"},
    }
    result = json.loads(schema_for_mcp({"$ref": "#/components/schemas/Request"}, components))
    assert set(result["$defs"]) == {"Request", "Node"}
    assert result["$defs"]["Node"]["properties"]["next"]["$ref"] == "#/$defs/Node"
    assert components["Node"]["properties"]["next"]["$ref"] == "#/components/schemas/Node"
    assert "$defs" not in json.loads(schema_for_mcp({"type": "object"}, components))
