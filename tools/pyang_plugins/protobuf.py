################################################################################
#                                                                              #
#  Copyright 2022 Broadcom. The term Broadcom refers to Broadcom Inc. and/or   #
#  its subsidiaries.                                                           #
#                                                                              #
#  Licensed under the Apache License, Version 2.0 (the "License");             #
#  you may not use this file except in compliance with the License.            #
#  You may obtain a copy of the License at                                     #
#                                                                              #
#     http://www.apache.org/licenses/LICENSE-2.0                               #
#                                                                              #
#  Unless required by applicable law or agreed to in writing, software         #
#  distributed under the License is distributed on an "AS IS" BASIS,           #
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.    #
#  See the License for the specific language governing permissions and         #
#  limitations under the License.                                              #
#                                                                              #
################################################################################

"""pyang plugin to convert a yang schema to a protobuf schema
"""
import optparse
import os
import sys
from io import StringIO
from collections import OrderedDict
from re import sub
from pyang import plugin, statements, util
from jinja2 import Environment, FileSystemLoader

# Register the Protobuf plugin
def pyang_plugin_init():
    plugin.register_plugin(ProtobufPlugin())

# Globals

def snake_to_camel(word):
    return sub(r"(_|-)+", " ", word).title().replace(" ", "")

current_proto = None
pyang_plugin_dir = os.path.dirname(os.path.realpath(__file__))
server_template_dir = os.path.join(pyang_plugin_dir, 'templates', 'server')
client_template_dir = os.path.join(pyang_plugin_dir, 'templates', 'client')

# nosemgrep: python.flask.security.xss.audit.direct-use-of-jinja2.direct-use-of-jinja2
server_template_env = Environment(loader=FileSystemLoader(server_template_dir), trim_blocks=True, lstrip_blocks=True)
# nosemgrep: python.flask.security.xss.audit.direct-use-of-jinja2.direct-use-of-jinja2
client_template_env = Environment(loader=FileSystemLoader(client_template_dir), trim_blocks=True, lstrip_blocks=True)

rpc_server_template = server_template_env.get_template('rpc.j2')
rpc_server_imports_template = server_template_env.get_template('rpc_imports.j2')
rpc_register_template = server_template_env.get_template('register.j2')
gnoiyang_template = server_template_env.get_template('gnoiyang.j2')

rpc_client_template = stub = client_template_env.get_template('main.j2')

class Protobuf(object):
    def __init__(self, module_name):
        module_name = module_name.replace('-','_')
        self.module_name = snake_to_camel(module_name)
        self.module_name_plain = module_name
        self.tree = OrderedDict()
        self.containers = []
        self.ylist = []
        self.leafs = []
        self.enums = []
        self.headers = []
        self.services = []
        self.rpcs = []
        self.has_empty = False
        self.has_value_type = False

    def set_headers(self):
        self.headers.append('syntax = "proto3";')
        self.headers.append(f'\npackage gnoi.{self.module_name};\n')
        if self.has_value_type:
            self.headers.append('import "google/protobuf/struct.proto";')

    def _print_rpc_server_stub(self, ctx):
        out = StringIO()
        # nosemgrep: python.flask.security.xss.audit.direct-use-of-jinja2.direct-use-of-jinja2
        out.write(rpc_server_imports_template.render(module_name=self.module_name,module_name_plain=self.module_name_plain))
        for rpc in self.rpcs:
            # nosemgrep: python.flask.security.xss.audit.direct-use-of-jinja2.direct-use-of-jinja2
            out.write(rpc_server_template.render(rpc_name=rpc.name_without_parent, rpc_url=rpc.rpc_url, rpc_input_empty=rpc.input_empty, rpc_output_empty=rpc.output_empty))
        fndir = os.path.join(ctx.opts.server_stub_outdir, self.module_name_plain)
        if fndir and not os.path.exists(fndir):
            os.makedirs(fndir)
        fn = os.path.join(fndir, self.module_name_plain+'.go')
        with open(fn, "w") as fh:
            fh.write(out.getvalue())

    def _print_rpc(self, out, level=0):
        spaces = '    ' * level
        out.write(''.join([spaces, f"service {self.module_name}Service "]))
        out.write(''.join('{\n'))

        rpc_space = spaces + '    '
        for rpc in self.rpcs:
            out.write(''.join([rpc_space, f"rpc {rpc.name_without_parent.replace('-','_')}({rpc.input.replace('-','_')}) returns({rpc.output.replace('-','_')})"]))
            out.write(''.join(' {}\n'))

        out.write(''.join([spaces, '}\n']))

    def _print_container(self, container, out, level=0):
        spaces = '    ' * level
        out.write(''.join([spaces, f"message {container.name.replace('-','_')} "]))
        out.write(''.join('{\n'))

        for l in container.ylist:
            self._print_list(l, out, level + 1)

        for inner in container.containers:
            self._print_container(inner, out, level + 1)

        self._print_leaf(container.leafs, out, spaces=spaces)

        out.write(''.join([spaces, '}\n']))

    def _print_list(self, ylist, out, level=0):
        spaces = '    ' * level
        out.write(''.join([spaces, f"message {ylist.name.replace('-', '_')} "]))
        out.write(''.join('{\n'))

        for l in ylist.ylist:
            self._print_list(l, out, level + 1)

        for inner in ylist.containers:
            self._print_container(inner, out, level + 1)

        self._print_leaf(ylist.leafs, out, spaces=spaces)

        out.write(''.join([spaces, '}\n']))


    def _print_leaf(self, leafs, out, spaces='', include_message=False):
        leafspaces = ''.join([spaces, '    '])
        for idx, l in enumerate(leafs):
            if l.type == "enum":
                out.write(''.join([leafspaces, f"enum {l.name.replace('-','_').capitalize()}\n"]))
                out.write(''.join([leafspaces, '{\n']))
                self._print_enumeration(l.enumeration, out, leafspaces)
                out.write(''.join([leafspaces, '}\n']))
                l.type = l.name.replace('-','_').capitalize()
            if include_message:
                out.write(''.join([spaces, f"message {l.name.replace('-','_')} "]))
                out.write(''.join([spaces, '{\n']))
            out.write(''.join([leafspaces, f'{"repeated " if l.leaf_list else ""}{l.type} {l.name.replace("-", "_")} = {idx + 1} [json_name = "{l.json_name}"];\n']))
            if include_message:
                out.write(''.join([spaces, '}\n']))

    def _print_enumeration(self, yang_enum, out, spaces):
        enumspaces = ''.join([spaces, '    '])
        for _, e in enumerate(yang_enum):
            out.write(''.join([enumspaces, f'{e}\n']))

    def print_proto(self):
        out = StringIO()
        for h in self.headers:
            out.write(f"{h}\n")
        out.write('\n')

        if self.leafs:
            self._print_leaf(self.leafs, out, spaces='', include_message=True)
            out.write('\n')

        if self.ylist:
            for l in self.ylist:
                self._print_list(l, out)
            out.write('\n')

        if self.containers:
            for c in self.containers:
                self._print_container(c, out)

            out.write('\n')

        if self.rpcs:
            self._print_rpc(out)

        return out


class YangContainer(object):
    def __init__(self):
        self.name = None
        self.containers = []
        self.enums = []
        self.leafs = []
        self.ylist = []

class YangList(object):
    def __init__(self):
        self.name = None
        self.leafs = []
        self.containers = []
        self.ylist = []


class YangLeaf(object):
    def __init__(self):
        self.name = None
        self.type = None
        self.json_name = None
        self.leaf_list = False
        self.enumeration = []
        self.enumeration_names = set()
        self.description = None


class YangEnumeration(object):
    def __init__(self):
        self.value = []


class YangRpc(object):
    def __init__(self):
        self.mod_name = None
        self.name = None
        self.name_without_parent = None
        self.input = 'google.protobuf.Empty'
        self.output = 'google.protobuf.Empty'
        self.input_empty = False
        self.output_empty = False
        self.url = None


class ProtobufPlugin(plugin.PyangPlugin):
    def add_output_format(self, fmts):
        self.multiple_modules = True
        fmts['proto'] = self

    def setup_fmt(self, ctx):
        ctx.implicit_errors = False

    def add_opts(self, optparser):
        optlist = [
            optparse.make_option("--proto-outdir",
                                 type="string",
                                 dest="outdir",
                                 help="Output directory for protobuffs"),
            optparse.make_option("--server-rpc-outdir",
                                 type="string",
                                 dest="server_stub_outdir",
                                 help="Output directory for server stubs"),
            optparse.make_option("--client-rpc-outdir",
                                 type="string",
                                 dest="client_outdir",
                                 help="Output directory for server stubs"),
        ]
        g = optparser.add_option_group("OpenApiPlugin options")
        g.add_options(optlist)

    def write_file(self, fn, content):
        fileChanged = True
        if os.path.isfile(fn):
            with open(fn) as fp:
                fileChanged = (fp.read() != content)
        if fileChanged:
            with open(fn, "w") as fp:
                print(f"writing file: {fn}")
                fp.write(content)
        else:
            print(f"file {fn} unchanged, skipped writing...")
        return fileChanged

    def emit(self, ctx, modules, fd):
        """Main control function.
        """
        opt_messages = {
            0: "--proto-outdir",
            1: "--server-rpc-outdir",
            2: "--client-rpc-outdir"
        }
        global current_proto
        self.ctx = ctx
        for idx, d in enumerate([ctx.opts.outdir, ctx.opts.server_stub_outdir, ctx.opts.client_outdir]):
            if not d:
                message = opt_messages.get(idx)
                if message:
                    print(f"--{message} cannot be empty")
                sys.exit(2)
            if d and not os.path.exists(d):
                os.makedirs(d)

        server_mods = []
        mod_name_map = OrderedDict()
        rpcs_list = []
        mods_rpc_map = OrderedDict()
        prefix = "openconfig"
        for module in modules:
            if module.keyword == "submodule":
                continue
            proto = Protobuf(module.i_modulename)
            current_proto = proto
            # Only looking for RPCs as of now.
            # Can be extended to other statements if required.
            rpcs = module.search('rpc', children=module.i_children)
            if len(rpcs) < 1:
                continue
            print("===> processing %s ..." % (module.i_modulename))
            if module.i_modulename.startswith("sonic") or module.i_modulename.startswith("Sonic"):
                prefix = "sonic"
            for rpc in rpcs:
                self.process_rpc(rpc, proto)
            proto.set_headers()
            proto_content = proto.print_proto().getvalue()
            if proto.module_name not in mods_rpc_map:
                mods_rpc_map[proto.module_name] = list()
            for rpc in proto.rpcs:
                rpcs_list.append(rpc)
                mods_rpc_map[proto.module_name].append(rpc)
            server_mods.append(proto.module_name)
            mod_name_map[proto.module_name] = proto.module_name_plain
            # check if file is same
            protoFnDir = os.path.join(ctx.opts.outdir, proto.module_name_plain)
            if protoFnDir and not os.path.exists(protoFnDir):
                os.makedirs(protoFnDir)
            protoFn = os.path.join(protoFnDir, proto.module_name_plain + ".proto")
            protoChanged = self.write_file(protoFn, proto_content)
            if protoChanged:
                    proto._print_rpc_server_stub(ctx)
            else:
                print("skip unchanged module: " + module.i_modulename)

        # nosemgrep: python.flask.security.xss.audit.direct-use-of-jinja2.direct-use-of-jinja2
        stub = rpc_register_template.render(modules=server_mods, prefix=prefix, mod_name_map=mod_name_map)
        fn = os.path.join(ctx.opts.server_stub_outdir, f'{prefix}_register.go')
        self.write_file(fn, stub)

        # nosemgrep: python.flask.security.xss.audit.direct-use-of-jinja2.direct-use-of-jinja2
        stub = gnoiyang_template.render()
        fn = os.path.join(ctx.opts.server_stub_outdir, 'gnoiyang.go')
        self.write_file(fn, stub)

        fndir = os.path.join(ctx.opts.client_outdir, f'gnoi_{prefix}_client')
        if fndir and not os.path.exists(fndir):
            os.makedirs(fndir)
        fn = os.path.join(fndir, 'main.go')
        client_stub = rpc_client_template.render(rpcs=rpcs_list, prefix=prefix, mods_rpc_map=mods_rpc_map, mod_name_map=mod_name_map)
        self.write_file(fn, client_stub)

    def process_children(self, node, parent, pmod):
        """Process all children of `node`, except "rpc" and "notification".
        """
        for ch in node.i_children:
            if ch.keyword in ["rpc"]:
                self.process_rpc(ch, parent)
            if ch.keyword in ["notification"]:
                continue
            if ch.keyword in ["choice", "case"]:
                self.process_children(ch, parent, pmod)
                continue
            if ch.i_module.i_modulename == pmod:
                nmod = pmod
            else:
                nmod = ch.i_module.i_modulename
            xpath = mk_path_str(ch, prefix_onchange=True, prefix_to_module=True)
            node_name = xpath.split("/")[-1]
            if ch.keyword in ["container", "grouping"]:
                c = YangContainer()
                c.name = snake_to_camel(ch.arg)
                self.process_children(ch, c, nmod)
                parent.containers.append(c)
                lc = YangLeaf()
                lc.type = c.name
                lc.name = ch.arg.replace('-','_')
                lc.json_name = node_name
                parent.leafs.append(lc)
                # self.process_container(ch, p, nmod)
            elif ch.keyword == "list":
                l = YangList()
                l.name = snake_to_camel(ch.arg)
                self.process_children(ch, l, nmod)
                parent.ylist.append(l)
                lc = YangLeaf()
                lc.leaf_list = True
                lc.type = l.name
                lc.name = ch.arg.replace('-','_')
                lc.json_name = node_name
                parent.leafs.append(lc)
            elif ch.keyword in ["leaf", "leaf-list"]:
                self.process_leaf(ch, parent, ch.keyword == "leaf-list", node_name)

    def process_leaf(self, node, parent, leaf_list=False, node_name=None):
        global current_proto
        # Leaf have specific sub statements
        p_type, stmt = self.get_protobuf_type(node)
        if p_type == "google.protobuf.Value":
            current_proto.has_value_type = True
        leaf = YangLeaf()
        leaf.name = node.arg.replace('-','-')
        leaf.json_name = node_name
        leaf.type = p_type
        if leaf.type == "enum":
            if not self.process_enumeration(stmt, leaf, parent):
                print(f"[INFO] - Due to protobuf limitation changing type to string from enum for leaf-{node_name}")
                leaf.type = "string"
        leaf.description = node.search_one("description")
        leaf.leaf_list = leaf_list
        parent.leafs.append(leaf)

    def process_enumeration(self, node, leaf, leaf_parent):
        enumeration_dict = OrderedDict()
        enums = node.search('enum')
        for enum in enums:
            if enum.arg[0].isdigit() or '-' in enum.arg:
                return False
            for sibling_leaf in leaf_parent.leafs:
                if sibling_leaf.type == "enum":
                    if enum.arg in sibling_leaf.enumeration_names:
                        return False
            val = enum.search_one('value')
            if val is not None:
                enumeration_dict[enum.arg] = int(val.arg)
            else:
                enumeration_dict[enum.arg] = '0'

        for key, value in enumerate(enumeration_dict):
            leaf.enumeration.append(f'{value} = {key} ;')
            leaf.enumeration_names.add(value)
        return True

    def process_rpc(self, node, parent):
        yrpc = YangRpc()
        yrpc.rpc_url = mk_path_str(node, prefix_onchange=True, prefix_to_module=True)
        yrpc.name = snake_to_camel(parent.module_name_plain + '_' + node.arg)  # name of rpc call
        yrpc.mod_name = parent.module_name
        yrpc.name_without_parent = snake_to_camel(node.arg)  # name of rpc call in plain form
        yrpc.input = snake_to_camel(node.arg + '_request')
        c_input = YangContainer()
        c_input.name = yrpc.input
        parent.containers.append(c_input)
        # look for input node
        input_node = node.search_one("input")
        if input_node and input_node.substmts:
            input = YangContainer()
            input.name = "Input"
            self.process_children(input_node, input, None)
            c_input.containers.append(input)
            leaf = YangLeaf()
            leaf.name = "input"
            xpath = mk_path_str(input_node, prefix_onchange=True, prefix_to_module=True)
            mod_name = xpath.split("/")[-1].split(":")[0]
            leaf.json_name = f"{mod_name}:input"
            leaf.type = input.name
            c_input.leafs.append(leaf)
        else:
            parent.has_empty = True
            yrpc.input_empty = True

        yrpc.output = snake_to_camel(node.arg + '_response')
        c_output = YangContainer()
        c_output.name = yrpc.output
        parent.containers.append(c_output)
        output_node = node.search_one("output")
        if output_node and output_node.substmts:
            output = YangContainer()
            output.name = "Output"
            self.process_children(output_node, output, None)
            c_output.containers.append(output)
            leaf = YangLeaf()
            leaf.name = "output"
            xpath = mk_path_str(output_node, prefix_onchange=True, prefix_to_module=True)
            mod_name = xpath.split("/")[-1].split(":")[0]
            leaf.json_name = f"{mod_name}:output"
            leaf.type = output.name
            c_output.leafs.append(leaf)
        else:
            parent.has_empty = True
            yrpc.output_empty = True
        parent.rpcs.append(yrpc)

    def get_protobuf_type(self, node):
        yang_type, stmt = self.get_type(node)
        yang_type = yang_type.replace('-','_')
        if yang_type in self.protobuf_types_map.keys():
            return self.protobuf_types_map[yang_type], stmt
        else:
            print(f"Error - No Proto type mapping for yang type {yang_type}")
            sys.exit(2)

    def get_type(self, node):

        base_types = ['int8', 'int16', 'int32', 'int64',
                    'uint8', 'uint16', 'uint32', 'uint64',
                    'decimal64', 'string', 'boolean', 'enumeration',
                    'bits', 'binary', 'leafref', 'identityref', 'empty',
                    'union', 'instance-identifier'
                    ]
        # Get Type of a node
        t = node.search_one('type')

        if node.keyword == "type":
            t = node

        while t.arg not in base_types:
            # chase typedef
            name = t.arg
            if name.find(":") == -1:
                prefix = None
            else:
                [prefix, name] = name.split(':', 1)
            if prefix is None or t.i_module.i_prefix == prefix:
                # check local typedefs
                pmodule = t.i_module
                typedef = statements.search_typedef(pmodule, name)  # typedef is defined at module level
                if typedef is None:
                    # typedef is defined in local hierarchy
                    typedef = statements.search_typedef(t, name)
            else:
                # this is a prefixed name, check the imported modules
                err = []
                pmodule = util.prefix_to_module(t.i_module, prefix, t.pos, err)
                if pmodule is None:
                    return
                typedef = statements.search_typedef(pmodule, name)

            if typedef is None:
                print("Typedef ", name,
                    " is not found, make sure all dependent modules are present")
                sys.exit(2)
            t = typedef.search_one('type')

        return self.handle_leafref(node) if t.arg == "leafref" else (t.arg, t)

    def handle_leafref(self, node):
        target_node = None
        if target_node is None:
            target_node = statements.validate_leafref_path(self.ctx, node, node.i_leafref.path_spec, node.i_leafref.path_)[0]
        if target_node.keyword in ["leaf", "leaf-list"]:
            return self.get_type(target_node)
        else:
            print("leafref not pointing to leaf/leaflist")
            sys.exit(2)

    protobuf_types_map = dict(
        binary='bytes',
        bits='bytes',
        boolean='bool',
        decimal64='sint64',
        empty='string',
        int8='int32',
        int16='int32',
        int32='int32',
        int64='int64',
        string='string',
        uint8='uint32',
        uint16='uint32',
        uint32='uint32',
        uint64='uint64',
        union='google.protobuf.Value',
        enumeration='enum',
        identityref='string',
        instance_identifier='string'
    )

def mk_path_list(stmt):
    """Derives a list of tuples containing
    (module name, prefix, xpath, keys)
    per node in the statement.
    """
    resolved_names = []
    def resolve_stmt(stmt, resolved_names):
        if stmt.keyword in ['case', 'input', 'output']:
            resolve_stmt(stmt.parent, resolved_names)
            return
        def qualified_name_elements(stmt):
            """(module name, prefix, name, keys)"""
            return (
                stmt.i_module.i_modulename,
                stmt.i_module.i_prefix,
                stmt.arg,
                get_keys(stmt)
            )
        if stmt.parent.keyword in ['module', 'submodule']:
            resolved_names.append(qualified_name_elements(stmt))
            return
        else:
            resolve_stmt(stmt.parent, resolved_names)
            resolved_names.append(qualified_name_elements(stmt))
            return
    resolve_stmt(stmt, resolved_names)
    return resolved_names

def get_keys(stmt):
    """Gets the key names for the node if present.
    Returns a list of key name strings.
    """
    key_obj = stmt.search_one('key')
    key_names = []
    keys = getattr(key_obj, 'arg', None)
    if keys:
        key_names = keys.split()
    return key_names

def mk_path_str(stmt,
                with_prefixes=False,
                prefix_onchange=False,
                prefix_to_module=False,
                resolve_top_prefix_to_module=False,
                with_keys=False):
    """Returns the XPath path of the node.
    with_prefixes indicates whether or not to prefix every node.

    prefix_onchange modifies the behavior of with_prefixes and
      only adds prefixes when the prefix changes mid-XPath.

    prefix_to_module replaces prefixes with the module name of the prefix.

    resolve_top_prefix_to_module resolves the module-level prefix
      to the module name.

    with_keys will include "[key]" to indicate the key names in the XPath.

    Prefixes may be included in the path if the prefix changes mid-path.
    """
    resolved_names = mk_path_list(stmt)
    xpath_elements = []
    last_prefix = None
    for index, resolved_name in enumerate(resolved_names):
        module_name, prefix, node_name, node_keys = resolved_name
        xpath_element = node_name
        if with_prefixes or (prefix_onchange and prefix != last_prefix):
            new_prefix = prefix
            if (prefix_to_module or
                (index == 0 and resolve_top_prefix_to_module)):
                new_prefix = module_name
            xpath_element = '%s:%s' % (new_prefix, node_name)
        if with_keys and node_keys:
            for node_key in node_keys:
                xpath_element = '%s[%s]' % (xpath_element, node_key)
        xpath_elements.append(xpath_element)
        last_prefix = prefix
    return '/%s' % '/'.join(xpath_elements)
