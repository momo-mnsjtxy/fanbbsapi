#!/usr/bin/env python3
import pathlib, sys, yaml

class UniqueLoader(yaml.SafeLoader):
    pass

def construct_mapping(loader, node, deep=False):
    mapping = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in mapping:
            raise ValueError(f"duplicate YAML key: {key}")
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping

UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, construct_mapping)
document = yaml.load(pathlib.Path(sys.argv[1]).read_text(), Loader=UniqueLoader)
assert document.get("openapi") == "3.1.0"
refs = []
def walk(value):
    if isinstance(value, dict):
        for key, child in value.items():
            if key == "$ref": refs.append(child)
            walk(child)
    elif isinstance(value, list):
        for child in value: walk(child)
walk(document)
for ref in refs:
    assert ref.startswith("#/"), f"non-local ref: {ref}"
    node = document
    for token in ref[2:].split('/'):
        node = node[token.replace('~1','/').replace('~0','~')]
operations = [method for path in document["paths"].values() for method in path if method.lower() in {"get","put","post","delete","patch"}]
print(f"OpenAPI {document['openapi']}: {len(document['paths'])} paths, {len(refs)} refs, {len(operations)} operations")
